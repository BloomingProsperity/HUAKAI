package protosse

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

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
