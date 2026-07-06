package protosse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/geminicodeassist"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

// ReconstructBufferedFromSSE 把一个 SSE 形态的缓冲上游响应体转换成缓冲式 HCSF
// 响应:逐条把每个 data 负载通过现有的上游流式 adapter 重放一遍。
func ReconstructBufferedFromSSE(adapter proto.UpstreamAdapter, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, bool) {
	if !looksLikeSSE(raw) {
		return nil, nil, false
	}
	if adapter == nil {
		return nil, nil, true
	}

	state := newUpstreamState(adapter)
	ctx := context.Background()
	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry
	for _, evt := range splitSSE(raw) {
		if len(bytes.TrimSpace(evt.data)) == 0 {
			continue
		}
		out, eventLosses, err := adapter.ProviderEventToCanonicalEvents(ctx, evt.data, state)
		losses = append(losses, eventLosses...)
		if err != nil {
			losses = append(losses, proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "buffered SSE event could not be reconstructed"))
			continue
		}
		events = append(events, canonicalEvents(out)...)
	}
	final, err := adapter.FinalizeUpstreamStream(ctx, state)
	if err != nil {
		losses = append(losses, proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "buffered SSE stream could not be finalized"))
	} else {
		events = append(events, canonicalEvents(final)...)
	}

	resp, foldLosses := responseFromEvents(events)
	losses = append(losses, foldLosses...)
	if !responseHasValue(resp) {
		return nil, losses, true
	}
	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &resp
	env.Accounting.Usage = resp.Usage
	if len(losses) > 0 {
		env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, losses...)
	}
	return env, losses, true
}

type sseEvent struct {
	event string
	data  []byte
}

func looksLikeSSE(raw []byte) bool {
	scanner := newSSEScanner(raw)
	for scanner.Scan() {
		line := bytes.TrimLeft(scanner.Bytes(), " \t")
		if bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte("event:")) {
			return true
		}
	}
	return false
}

func splitSSE(raw []byte) []sseEvent {
	scanner := newSSEScanner(raw)
	var events []sseEvent
	var current sseEvent
	var data bytes.Buffer
	flush := func() {
		if current.event == "" && data.Len() == 0 {
			return
		}
		current.data = append([]byte(nil), bytes.TrimSpace(data.Bytes())...)
		events = append(events, current)
		current = sseEvent{}
		data.Reset()
	}
	for scanner.Scan() {
		line := bytes.TrimRight(scanner.Bytes(), "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			flush()
			continue
		}
		line = bytes.TrimLeft(line, " \t")
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			current.event = string(trimSSEFieldValue(bytes.TrimPrefix(line, []byte("event:"))))
		case bytes.HasPrefix(line, []byte("data:")):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.Write(trimSSEFieldValue(bytes.TrimPrefix(line, []byte("data:"))))
		}
	}
	flush()
	return events
}

func newSSEScanner(raw []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	maxTokenSize := len(raw) + 1
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	}
	scanner.Buffer(make([]byte, 0, 4096), maxTokenSize)
	return scanner
}

func trimSSEFieldValue(v []byte) []byte {
	if len(v) > 0 && v[0] == ' ' {
		v = v[1:]
	}
	return bytes.TrimRight(v, "\r")
}

// newUpstreamState 与 gateway/forwarder.newUpstreamState 是同一类型分派的
// 孪生站点(族集对称第 8 站):新 proto adapter 落地时两处都要加 case,
// 否则 SSE 兜底重组对该族 type-assert 失败、整族不可恢复。
func newUpstreamState(adapter proto.UpstreamAdapter) any {
	switch adapter.(type) {
	case *openai.Adapter:
		return &openai.UpstreamState{}
	case *openai.ResponsesAdapter:
		return &openai.ResponsesUpstreamState{}
	case *gemini.Adapter:
		return &gemini.UpstreamState{}
	case *geminicodeassist.Adapter:
		// 族集对称第 8 站(gateway/forwarder.newUpstreamState 的孪生):
		// geminicodeassist.Adapter 委托内嵌 gemini.Adapter,需 gemini 的 state
		// 类型;漏此 case 落 default=anthropic state,SSE 兜底重组对该族
		// type-assert *gemini.UpstreamState 失败、不可恢复。
		return &gemini.UpstreamState{}
	case *dify.Adapter:
		return &dify.UpstreamState{}
	case *ollama.Adapter:
		// 族集对称第 8 站(gateway/forwarder.newUpstreamState 的孪生):漏此
		// case 时 ollama adapter type-assert *ollama.UpstreamState 失败,
		// SSE 兜底重组对该族不可恢复。
		return &ollama.UpstreamState{}
	default:
		return &anthropic.UpstreamState{}
	}
}

func canonicalEvents(in []any) []proto.CanonicalEvent {
	out := make([]proto.CanonicalEvent, 0, len(in))
	for _, item := range in {
		switch evt := item.(type) {
		case proto.CanonicalEvent:
			out = append(out, evt)
		case *proto.CanonicalEvent:
			if evt != nil {
				out = append(out, *evt)
			}
		}
	}
	return out
}

type blockState struct {
	block proto.CanonicalContentBlock
	input strings.Builder
}

func responseFromEvents(events []proto.CanonicalEvent) (proto.CanonicalResponse, []proto.ProtocolLossEntry) {
	resp := proto.CanonicalResponse{}
	blocks := map[int]*blockState{}
	var order []int
	var losses []proto.ProtocolLossEntry
	var sawMessageStart bool
	var contentBeforeMessageStart bool

	ensureBlock := func(index int, typ string) *blockState {
		if b, ok := blocks[index]; ok {
			if b.block.Type == "" {
				b.block.Type = typ
			}
			return b
		}
		b := &blockState{block: proto.CanonicalContentBlock{Type: typ}}
		blocks[index] = b
		order = append(order, index)
		return b
	}

	for _, evt := range events {
		switch evt.Type {
		case "message_start":
			sawMessageStart = true
			if evt.MessageID != "" {
				resp.ID = evt.MessageID
			}
			if evt.Model != "" {
				resp.Model = evt.Model
			}
			if evt.Usage != nil {
				resp.Usage = *evt.Usage
			}
		case "content_block_start":
			if !sawMessageStart {
				contentBeforeMessageStart = true
			}
			if evt.ContentBlock == nil {
				continue
			}
			b := ensureBlock(evt.Index, evt.ContentBlock.Type)
			b.block = *evt.ContentBlock
		case "content_block_delta":
			if !sawMessageStart {
				contentBeforeMessageStart = true
			}
			if evt.Delta == nil {
				continue
			}
			switch evt.Delta.Type {
			case "text_delta":
				b := ensureBlock(evt.Index, "text")
				b.block.Text += evt.Delta.Text
			case "tool_input_delta":
				b := ensureBlock(evt.Index, "tool_use")
				b.input.WriteString(partialJSONText(evt.Delta.PartialJSON))
			}
		case "message_delta":
			if evt.Usage != nil {
				// message_delta 顶层 usage 往往只带新的 output_tokens,
				// input/cache_read/cache_creation 仅在 message_start 出现。
				// 此处若整段覆盖 resp.Usage,会把 message_start 写入的 input/cache
				// token 抹成 0,使缓冲重组响应静默少计费(input 常是缓存重/长上下文
				// 流量的主成本)。改为逐字段 set-if-nonzero 合并:message_delta
				// 真带了新的 input/cache 时仍更新,只带 output 时则保住既有 input/cache。
				mergeNonZeroUsage(&resp.Usage, *evt.Usage)
			}
			if evt.StopReason != "" {
				resp.StopReason = evt.StopReason
			}
		}
	}
	if contentBeforeMessageStart {
		losses = append(losses, proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "buffered SSE content appeared before message_start"))
		return proto.CanonicalResponse{}, losses
	}

	for _, index := range order {
		b := blocks[index]
		if b.block.Type == "tool_use" && len(b.block.Input) == 0 && b.input.Len() > 0 {
			raw := []byte(b.input.String())
			if json.Valid(raw) {
				b.block.Input = append(json.RawMessage(nil), raw...)
			} else {
				quoted, _ := json.Marshal(b.input.String())
				b.block.Input = quoted
				losses = append(losses, proto.NewLossEntry(proto.FeatureToolUse, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "streamed tool input was not complete JSON"))
			}
		}
		resp.Content = append(resp.Content, b.block)
	}
	// 回填总账。除了 total 缺失,还要处理"total 是上游 message_delta 用被清零的
	// base 算出的陈旧值"(只反映 output、漏掉 message_start 的 input)的情形:
	// 此时陈旧 total 会小于已恢复的 input+output,按可见字段重算才不少计费。
	if recomputed := resp.Usage.InputTokens + resp.Usage.OutputTokens; resp.Usage.TotalTokens < recomputed {
		resp.Usage.TotalTokens = recomputed
	}
	return resp, losses
}

// mergeNonZeroUsage 把 src 的非零 token 字段并入 dst,保留 dst 已有的非零值。
// 用于缓冲 SSE 重组:message_start 先填入 input/cache 等字段,后续 message_delta
// 通常只带新的 output_tokens,合并时不能用零值覆盖既有字段,否则 input/cache 被
// 抹零导致少计费。语义与活流式 usage 累加器一致:仅在 src 字段非零时写入。
// tool-call 计数为按次累加(每次调用一帧),与 token 的"取最新非零"不同。
func mergeNonZeroUsage(dst *proto.CanonicalUsage, src proto.CanonicalUsage) {
	if src.InputTokens != 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.ReasoningTokens != 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
	if src.TotalTokens != 0 {
		dst.TotalTokens = src.TotalTokens
	}
	if src.CacheCreationInputTokens != 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheCreationInputTokens5m != 0 {
		dst.CacheCreationInputTokens5m = src.CacheCreationInputTokens5m
	}
	if src.CacheCreationInputTokens1h != 0 {
		dst.CacheCreationInputTokens1h = src.CacheCreationInputTokens1h
	}
	if src.CacheReadInputTokens != 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	dst.WebSearchCalls += src.WebSearchCalls
	dst.FileSearchCalls += src.FileSearchCalls
	dst.ImageGenerationCalls += src.ImageGenerationCalls
}

func partialJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func responseHasValue(resp proto.CanonicalResponse) bool {
	return resp.ID != "" || resp.Model != "" || len(resp.Content) > 0 ||
		resp.StopReason != "" || proto.UsageHasValue(resp.Usage)
}
