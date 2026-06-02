package protosse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

// ReconstructBufferedFromSSE converts an SSE-shaped buffered upstream body into
// a buffered HCSF response by replaying each data payload through the existing
// upstream streaming adapter.
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

func newUpstreamState(adapter proto.UpstreamAdapter) any {
	switch adapter.(type) {
	case *openai.Adapter:
		return &openai.UpstreamState{}
	case *gemini.Adapter:
		return &gemini.UpstreamState{}
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
				resp.Usage = *evt.Usage
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
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	return resp, losses
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
