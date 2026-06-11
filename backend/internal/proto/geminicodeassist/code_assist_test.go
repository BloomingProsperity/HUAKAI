package geminicodeassist

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
)

var _ proto.UpstreamAdapter = (*Adapter)(nil)

// geminiBody 是一个最小但有判别力的 gemini 非流式响应:含文本 + STOP + usage。
const geminiBody = `{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"index":0,"finishReason":"STOP"}],"modelVersion":"gemini-2.5-pro","responseId":"resp-9","usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`

// TestProviderResponseUnwrapsResponseEnvelope 守卫承重:cloudcode-pa 非流式 body
// 包 {"response":{<gemini body>}},unwrap 后与裸 gemini body 经 gemini.Adapter
// 解出相同 canonical。Mutation:删 unwrap(直接喂 {response:...} 给 gemini)→
// gemini 找不到 candidates,内容/usage 全空 → 与基线 diff → 红。
func TestProviderResponseUnwrapsResponseEnvelope(t *testing.T) {
	wrapped := []byte(`{"response":` + geminiBody + `}`)

	ca := &Adapter{}
	caEnv, _, err := ca.ProviderResponseToCanonical(context.Background(), wrapped)
	if err != nil {
		t.Fatalf("code-assist ProviderResponseToCanonical err=%v", err)
	}

	// 基线:裸 gemini body 经原生 gemini.Adapter。两者必须等价。
	base := &gemini.Adapter{}
	baseEnv, _, err := base.ProviderResponseToCanonical(context.Background(), []byte(geminiBody))
	if err != nil {
		t.Fatalf("baseline gemini ProviderResponseToCanonical err=%v", err)
	}

	caText := bufferedText(t, caEnv)
	baseText := bufferedText(t, baseEnv)
	if caText != "hello" || caText != baseText {
		t.Fatalf("unwrap mismatch: codeAssist text=%q baseline=%q want hello", caText, baseText)
	}
	if caEnv.BufferedResponse == nil || caEnv.BufferedResponse.Usage.TotalTokens != 5 {
		t.Fatalf("usage lost after unwrap: %+v", caEnv.BufferedResponse)
	}
}

// TestProviderResponseDiscriminatesAgainstNoUnwrap 直接证明判别性:把 wrapped
// body 喂给裸 gemini.Adapter(模拟漏 unwrap)产出的 canonical 与 code-assist
// adapter(有 unwrap)产出的不同——前者无文本,后者有 "hello"。
func TestProviderResponseDiscriminatesAgainstNoUnwrap(t *testing.T) {
	wrapped := []byte(`{"response":` + geminiBody + `}`)

	noUnwrap := &gemini.Adapter{}
	noEnv, _, err := noUnwrap.ProviderResponseToCanonical(context.Background(), wrapped)
	if err != nil {
		t.Fatalf("baseline err=%v", err)
	}
	if got := bufferedText(t, noEnv); got == "hello" {
		t.Fatalf("漏 unwrap 不应解出文本(判别性失效),got %q", got)
	}

	ca := &Adapter{}
	caEnv, _, err := ca.ProviderResponseToCanonical(context.Background(), wrapped)
	if err != nil {
		t.Fatalf("code-assist err=%v", err)
	}
	if got := bufferedText(t, caEnv); got != "hello" {
		t.Fatalf("有 unwrap 应解出 hello,got %q", got)
	}
}

// TestProviderResponseToleratesAlreadyUnwrapped 守卫容忍:已 unwrap 的裸 gemini
// body(无 "response" 字段)原样解析,不报错。
func TestProviderResponseToleratesAlreadyUnwrapped(t *testing.T) {
	ca := &Adapter{}
	env, _, err := ca.ProviderResponseToCanonical(context.Background(), []byte(geminiBody))
	if err != nil {
		t.Fatalf("already-unwrapped err=%v", err)
	}
	if got := bufferedText(t, env); got != "hello" {
		t.Fatalf("tolerate already-unwrapped: text=%q want hello", got)
	}
}

// TestProviderEventUnwrapsPerChunk 守卫每个 SSE chunk 的 {response} unwrap:
// 包层 chunk 经 code-assist adapter → 解出 text delta;Mutation:删 per-chunk
// unwrap → gemini 解 {response:...} 无 candidates → 无 text 事件 → 红。
func TestProviderEventUnwrapsPerChunk(t *testing.T) {
	chunk := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"chunk1"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro","responseId":"r1"}}`)

	ca := &Adapter{}
	state := &gemini.UpstreamState{}
	out, _, err := ca.ProviderEventToCanonicalEvents(context.Background(), chunk, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents err=%v", err)
	}
	if !hasTextDelta(out, "chunk1") {
		t.Fatalf("unwrap 后应有 text_delta=chunk1,got %v", out)
	}

	// 判别性:同 chunk 喂裸 gemini(漏 unwrap)无 text。
	bare := &gemini.Adapter{}
	bareState := &gemini.UpstreamState{}
	bareOut, _, _ := bare.ProviderEventToCanonicalEvents(context.Background(), chunk, bareState)
	if hasTextDelta(bareOut, "chunk1") {
		t.Fatalf("漏 unwrap 不应解出 chunk1(判别性失效)")
	}
}

// TestProviderEventViaSSEEvent 守卫 forwarder 经 gemini.SSEEvent 形态喂入时也
// unwrap(scanner 已剥 data: 前缀,Data 为裸 JSON)。
func TestProviderEventViaSSEEvent(t *testing.T) {
	evt := gemini.SSEEvent{Data: []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"sse-chunk"}],"role":"model"},"index":0}],"responseId":"r2"}}`)}
	ca := &Adapter{}
	state := &gemini.UpstreamState{}
	out, _, err := ca.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if err != nil {
		t.Fatalf("SSEEvent path err=%v", err)
	}
	if !hasTextDelta(out, "sse-chunk") {
		t.Fatalf("SSEEvent unwrap 后应有 text_delta=sse-chunk,got %v", out)
	}
}

// TestFinalizeAndCanonicalRequestPassthrough 守卫请求侧 + finalize 透传 inner:
// CanonicalToProviderRequest 委托 inner(gemini 返回 ErrNotImplemented),
// FinalizeUpstreamStream 透传 inner 产出终态事件。
func TestFinalizeAndCanonicalRequestPassthrough(t *testing.T) {
	ca := &Adapter{}
	if _, _, err := ca.CanonicalToProviderRequest(context.Background(), &proto.HCSF{}); err == nil {
		t.Fatalf("CanonicalToProviderRequest 应透传 inner 的 ErrNotImplemented")
	}
	// finalize after consuming a chunk should not panic and should produce events.
	state := &gemini.UpstreamState{}
	chunk := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}}`)
	if _, _, err := ca.ProviderEventToCanonicalEvents(context.Background(), chunk, state); err != nil {
		t.Fatalf("consume chunk err=%v", err)
	}
	final, err := ca.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream err=%v", err)
	}
	if len(final) == 0 {
		t.Fatalf("finalize 应产出终态事件(message_stop 等)")
	}
}

func bufferedText(t *testing.T, env *proto.HCSF) string {
	t.Helper()
	if env == nil || env.BufferedResponse == nil {
		return ""
	}
	var s string
	for _, b := range env.BufferedResponse.Content {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

func hasTextDelta(out []any, want string) bool {
	for _, e := range out {
		ev, ok := e.(proto.CanonicalEvent)
		if !ok {
			continue
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text == want {
			return true
		}
	}
	return false
}
