package protosse

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

// TestReconstructBufferedFromSSEDifyStream 守卫族集对称第 8 站:本包
// newUpstreamState 与 gateway/forwarder.newUpstreamState 是孪生类型分派,
// dify case 缺失时 dify adapter type-assert *dify.UpstreamState 失败,每个
// 事件进 loss、重组永远失败——blocking 请求遇上游回 SSE 时整族不可恢复。
// Mutation:删 newUpstreamState 的 dify case → 本测试红。
func TestReconstructBufferedFromSSEDifyStream(t *testing.T) {
	raw := []byte(`
data: {"event":"message","conversation_id":"conv-1","answer":"rescued dify text"}

data: {"event":"message_end","conversation_id":"conv-1","metadata":{"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}}
`)
	env, _, ok := ReconstructBufferedFromSSE(&dify.Adapter{}, raw)
	if !ok {
		t.Fatal("dify SSE body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued dify text" {
		t.Fatalf("reconstructed content = %+v, want one text block", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 7 || got.OutputTokens != 3 {
		t.Fatalf("reconstructed usage = %+v, want input=7 output=3", got)
	}
}

// TestReconstructBufferedFromSSEOllamaStream 守卫族集对称第 8 站:
// newUpstreamState 漏 ollama case 时 ollama adapter type-assert
// *ollama.UpstreamState 失败,每个事件进 loss、重组永远失败——blocking 请求
// 遇代理把 NDJSON 包成 SSE 回传时整族不可恢复。
// Mutation:删 newUpstreamState 的 ollama case → 本测试红。
func TestReconstructBufferedFromSSEOllamaStream(t *testing.T) {
	raw := []byte(`
data: {"model":"llama3.2","message":{"role":"assistant","content":"rescued ollama text"},"done":false}

data: {"model":"llama3.2","done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}
`)
	env, _, ok := ReconstructBufferedFromSSE(&ollama.Adapter{}, raw)
	if !ok {
		t.Fatal("ollama SSE-shaped body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued ollama text" {
		t.Fatalf("reconstructed content = %+v, want one text block", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 7 || got.OutputTokens != 3 {
		t.Fatalf("reconstructed usage = %+v, want input=7 output=3", got)
	}
}

func TestReconstructBufferedFromSSEOpenAITextAndUsage(t *testing.T) {
	// Removing the SSE sniff/fallback makes this test go red: the raw body is
	// not one JSON object, so the normal buffered parser cannot recover content
	// or usage from it.
	raw := []byte(`
data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"rescued text"},"finish_reason":null}]}

data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]
`)

	env, losses, ok := ReconstructBufferedFromSSE(&openai.Adapter{}, raw)
	if !ok {
		t.Fatal("SSE body must be detected and reconstructed")
	}
	if len(losses) != 0 {
		t.Fatalf("happy path should not emit losses: %+v", losses)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued text" {
		t.Fatalf("reconstructed content = %+v, want one text block with rescued text", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("reconstructed usage = %+v, want input=10 output=5", got)
	}
}

func TestReconstructBufferedFromSSEPlainJSONDoesNotTrigger(t *testing.T) {
	raw := []byte(`{"id":"chatcmpl-json","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"plain json"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)

	env, losses, ok := ReconstructBufferedFromSSE(&openai.Adapter{}, raw)
	if ok {
		t.Fatalf("plain JSON body must not trigger SSE fallback: env=%+v losses=%+v", env, losses)
	}
	if env != nil || losses != nil {
		t.Fatalf("non-SSE body must return nil envelope/losses, got env=%+v losses=%+v", env, losses)
	}
}

func TestReconstructBufferedFromSSEMissingMessageStartDoesNotReturnResponse(t *testing.T) {
	// Risk killed: buffered fallback must not turn an Anthropic content delta
	// without message_start into a successful response. Mutation self-check:
	// removing the content-before-start guard returns a BufferedResponse here.
	raw := []byte(strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`,
		``,
		``,
	}, "\n"))

	env, losses, ok := ReconstructBufferedFromSSE(&anthropic.Adapter{}, raw)
	if !ok {
		t.Fatal("SSE-shaped body must be recognized")
	}
	if env != nil && env.BufferedResponse != nil {
		t.Fatalf("missing message_start reconstructed response: %+v", env.BufferedResponse)
	}
	if len(losses) == 0 {
		t.Fatal("missing message_start should emit reconstruction loss evidence")
	}
}
