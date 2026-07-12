// Package warmupintercept 在网关本地识别并应答 Claude Code 的"一次性"请求
// (连通性探测 / 标题生成预热 / SUGGESTION MODE),不让它们消耗池内上游账号
// 的真实配额。
//
// 拦截的三种形状是 Claude Code 客户端的可观测行为事实(机制参照 sub2api 的
// 同类能力,实现为本仓独立编写;行为对齐 + 客户端可见面更拟真):
//
//  1. 连通性探测: max_tokens=1 + haiku 系模型 + 非流式 + claude-cli UA,
//     真实上游会回一个 "#"(stop_reason=max_tokens)。
//  2. SUGGESTION MODE: 末条 user 消息文本以 "[SUGGESTION MODE:" 开头,
//     期望空文本应答。
//  3. 预热/标题生成: 消息文本含 5-10 词标题生成指令或恰为 "Warmup",
//     或 system 含"新话题判定+2-3 词标题抽取"指令,期望 "New Conversation"。
//
// 开关(warmup_intercept_enabled,默认关)在调用方;本包只负责识别与合成。
// 合成应答的消息 ID 一律随机生成为 Anthropic 直连形态(msg_01 + base62),
// 客户端可见面不留 mock 指纹。
package warmupintercept

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Kind 是被拦截请求的类型。
type Kind int

const (
	KindNone       Kind = iota
	KindConnProbe       // 连通性探测(max_tokens=1 + haiku + 非流式)
	KindSuggestion      // [SUGGESTION MODE: ...] 末条 user 消息
	KindWarmup          // 预热 / 标题生成
)

// Claude Code 一次性请求里出现的精确标记串(客户端 prompt 原文,事实数据)。
// 预扫描只认这些精确串,避免泛关键字误伤正常对话再走全量解析。
const (
	markerSuggestion  = "[SUGGESTION MODE:"
	markerTitlePrompt = "Please write a 5-10 word title for the following conversation:"
	markerWarmupQuote = `"Warmup"`
	markerTopicProbe  = "nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title"
)

// probeDoc 是检测所需的最小请求投影(具名类型,只取文本块)。
type probeDoc struct {
	Messages []probeMessage `json:"messages"`
	System   []probeText    `json:"system"`
}

type probeMessage struct {
	Role    string      `json:"role"`
	Content []probeText `json:"content"`
}

type probeText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// firstText 返回消息首个 text 块的文本;无 text 块返回空串。
func (m probeMessage) firstText() string {
	for _, b := range m.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

// IsClaudeCodeUserAgent 报告 User-Agent 是否为 Claude Code(claude-cli)客户端。
func IsClaudeCodeUserAgent(userAgent string) bool {
	return strings.Contains(strings.ToLower(userAgent), "claude-cli/")
}

// Detect 把请求分类为三种拦截形状之一。
//
//   - isClaudeCodeUA: 调用方传 IsClaudeCodeUserAgent(r.UserAgent())
//   - model / maxTokens / stream: 来自已解析的顶层字段
//   - body: 原始请求体(只在预扫描命中标记串时才做结构化解析)
func Detect(isClaudeCodeUA bool, model string, maxTokens int, stream bool, body []byte) (Kind, bool) {
	// 形状 1:连通性探测只看顶层字段 + UA,无需碰 body。
	if isClaudeCodeUA && maxTokens == 1 && !stream &&
		strings.Contains(strings.ToLower(model), "haiku") {
		return KindConnProbe, true
	}

	// 预扫描:四个精确标记串一个都不沾的请求直接放行,不付解析成本。
	wantSuggestion := bytes.Contains(body, []byte(markerSuggestion))
	wantWarmup := bytes.Contains(body, []byte(markerTitlePrompt)) ||
		bytes.Contains(body, []byte(markerWarmupQuote)) ||
		bytes.Contains(body, []byte(markerTopicProbe))
	if !wantSuggestion && !wantWarmup {
		return KindNone, false
	}

	var doc probeDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return KindNone, false
	}

	// 形状 2:末条消息必须是 user 且其文本以标记串开头(结构化确认,
	// 防止标记串只是出现在历史/引用里)。
	if wantSuggestion && len(doc.Messages) > 0 {
		if last := doc.Messages[len(doc.Messages)-1]; last.Role == "user" &&
			strings.HasPrefix(last.firstText(), markerSuggestion) {
			return KindSuggestion, true
		}
	}

	// 形状 3:任一消息文本含标题指令或恰为 "Warmup";或 system 含话题判定指令。
	if wantWarmup {
		for _, m := range doc.Messages {
			for _, b := range m.Content {
				if b.Type != "text" {
					continue
				}
				if strings.Contains(b.Text, markerTitlePrompt) || b.Text == "Warmup" {
					return KindWarmup, true
				}
			}
		}
		for _, s := range doc.System {
			if strings.Contains(s.Text, markerTopicProbe) {
				return KindWarmup, true
			}
		}
	}

	return KindNone, false
}

// syntheticSpec 描述每种拦截形状的应答内容。
type syntheticSpec struct {
	text       string
	stopReason string
	deltas     []string
	outTokens  int
}

func specFor(kind Kind) syntheticSpec {
	switch kind {
	case KindConnProbe:
		// 真实上游对 max_tokens=1 探测回单字符并以 max_tokens 截断。
		return syntheticSpec{text: "#", stopReason: "max_tokens", deltas: []string{"#"}, outTokens: 1}
	case KindSuggestion:
		return syntheticSpec{text: "", stopReason: "end_turn", deltas: []string{""}, outTokens: 1}
	default: // KindWarmup
		return syntheticSpec{text: "New Conversation", stopReason: "end_turn", deltas: []string{"New", " Conversation"}, outTokens: 2}
	}
}

const syntheticInputTokens = 10

// newMessageID 生成 Anthropic 直连形态的消息 ID(msg_01 + 22 位 base62)。
// 所有拦截形状统一用随机 ID——客户端可见面不留可指纹化的固定 mock ID。
func newMessageID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const tail = 22
	buf := make([]byte, tail)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("msg_01fb%016x", time.Now().UnixNano())
	}
	for i, c := range buf {
		buf[i] = alphabet[int(c)%len(alphabet)]
	}
	return "msg_01" + string(buf)
}

// 应答体用具名类型 Marshal,字段形状对齐 Anthropic Messages API(API 形状
// 本身是事实;含 cache_creation 细分,与真实应答同构)。
type syntheticUsage struct {
	InputTokens         int            `json:"input_tokens"`
	CacheCreationInput  int            `json:"cache_creation_input_tokens"`
	CacheReadInput      int            `json:"cache_read_input_tokens"`
	CacheCreationDetail map[string]int `json:"cache_creation"`
	OutputTokens        int            `json:"output_tokens"`
	TotalTokens         int            `json:"total_tokens"`
}

func newSyntheticUsage(outTokens int) syntheticUsage {
	return syntheticUsage{
		InputTokens: syntheticInputTokens,
		CacheCreationDetail: map[string]int{
			"ephemeral_5m_input_tokens": 0,
			"ephemeral_1h_input_tokens": 0,
		},
		OutputTokens: outTokens,
		TotalTokens:  syntheticInputTokens + outTokens,
	}
}

type syntheticMessage struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Model        string          `json:"model"`
	Content      []probeText     `json:"content"`
	StopReason   *string         `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence"`
	Usage        *syntheticUsage `json:"usage,omitempty"`
}

// SyntheticNonStreamBody 返回合成的非流式 Anthropic Messages 应答体;status
// 恒为 200。
func SyntheticNonStreamBody(kind Kind, model string) (status int, body []byte) {
	spec := specFor(kind)
	usage := newSyntheticUsage(spec.outTokens)
	msg := syntheticMessage{
		ID:         newMessageID(),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    []probeText{{Type: "text", Text: spec.text}},
		StopReason: &spec.stopReason,
		Usage:      &usage,
	}
	enc, err := json.Marshal(msg)
	if err != nil {
		enc = []byte(`{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`)
	}
	return http.StatusOK, enc
}

// sseEvent 以 "event: <name>\ndata: <json>\n\n" 形态追加一个 SSE 事件。
func sseEvent(sb *strings.Builder, name string, payload any) {
	enc, err := json.Marshal(payload)
	if err != nil {
		return
	}
	sb.WriteString("event: ")
	sb.WriteString(name)
	sb.WriteString("\ndata: ")
	sb.Write(enc)
	sb.WriteString("\n\n")
}

// SyntheticStreamBody 构造合成的流式(SSE)Anthropic Messages 应答字节。
func SyntheticStreamBody(kind Kind, model string) []byte {
	spec := specFor(kind)

	var sb strings.Builder
	sseEvent(&sb, "message_start", map[string]any{
		"type": "message_start",
		"message": syntheticMessage{
			ID:      newMessageID(),
			Type:    "message",
			Role:    "assistant",
			Model:   model,
			Content: []probeText{},
			Usage:   &syntheticUsage{InputTokens: syntheticInputTokens, CacheCreationDetail: map[string]int{}},
		},
	})
	sseEvent(&sb, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": probeText{Type: "text", Text: ""},
	})
	for _, delta := range spec.deltas {
		sseEvent(&sb, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": delta},
		})
	}
	sseEvent(&sb, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	sseEvent(&sb, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": spec.stopReason, "stop_sequence": nil},
		"usage": map[string]int{"input_tokens": syntheticInputTokens, "output_tokens": spec.outTokens},
	})
	sseEvent(&sb, "message_stop", map[string]any{"type": "message_stop"})
	return []byte(sb.String())
}

// WriteNonStream 将合成的非流式应答写入 w。
func WriteNonStream(w http.ResponseWriter, kind Kind, model string) {
	status, body := SyntheticNonStreamBody(kind, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// WriteStream 将合成的 SSE 流式应答写入 w。
func WriteStream(w http.ResponseWriter, kind Kind, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(SyntheticStreamBody(kind, model))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
