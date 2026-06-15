package realtokenizer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/tiktoken-go/tokenizer"
)

// An OpenAI-family model must count its plain-text JSON leaves with tiktoken, not
// the byte/char heuristic. The expected value is recomputed independently from the
// library so the assertion is self-proving and not a brittle magic number.
// MUTATION: make codecForModel return (nil,false) for gpt models (so InputTokens
// falls back to the heuristic) and got != want -> RED.
func TestInputTokens_OpenAIModelUsesTiktoken(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog, repeatedly and at some length."
	body := []byte(fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":%q}]}`, text))

	codec, err := tokenizer.ForModel("gpt-4o")
	if err != nil {
		t.Fatalf("ForModel gpt-4o: %v", err)
	}
	// The JSON walk counts every string VALUE leaf: the model name, the role, and
	// the content. Keys are not counted.
	want := 0
	for _, leaf := range []string{"gpt-4o", "user", text} {
		n, err := codec.Count(leaf)
		if err != nil {
			t.Fatalf("Count(%q): %v", leaf, err)
		}
		want += n
	}

	got := InputTokens("gpt-4o", body)
	if got != want {
		t.Fatalf("InputTokens(gpt-4o)=%d; want tiktoken sum %d", got, want)
	}
	// And it must differ from the heuristic — otherwise the test couldn't tell the
	// real tokenizer apart from the fallback.
	if heur := tokencheck.EstimateRequestInputTokens(body); got == heur {
		t.Fatalf("tiktoken count %d coincidentally equals heuristic %d; pick a more discriminating fixture", got, heur)
	}
}

// A non-OpenAI model has no tiktoken encoder, so InputTokens must use the shared
// heuristic exactly. MUTATION: if a Claude model wrongly resolved a codec, the
// result would diverge from the heuristic -> RED.
func TestInputTokens_NonOpenAIFallsBackToHeuristic(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello there"}]}`)

	if _, ok := textCounter("claude-3-5-sonnet-20241022"); ok {
		t.Fatal("claude model must not resolve a tiktoken codec")
	}
	got := InputTokens("claude-3-5-sonnet-20241022", body)
	want := tokencheck.EstimateRequestInputTokens(body)
	if got != want {
		t.Fatalf("non-OpenAI InputTokens=%d; want heuristic %d", got, want)
	}
}

// A multimodal request must never feed a base64 blob to the tokenizer: the blob is
// capped by tokencheck, so even an OpenAI model's estimate stays bounded far below
// the raw byte/4 figure. MUTATION: route blobs through the counter (drop the cap)
// and the estimate explodes toward len(blob)/4 -> RED.
func TestInputTokens_BlobIsCappedNotTokenized(t *testing.T) {
	blob := "data:image/png;base64," + strings.Repeat("A", 40000) // ~40KB; byte/4 ~= 10000
	body := []byte(fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":%q}}]}]}`, blob))

	got := InputTokens("gpt-4o", body)
	if got >= len(blob)/4 {
		t.Fatalf("blob estimate=%d not capped (raw byte/4=%d)", got, len(blob)/4)
	}
	if got > 4000 {
		t.Fatalf("blob estimate=%d unexpectedly large; cap not applied", got)
	}
}

// MUTATION: flip any branch of parseEnabled and one of these goes RED.
func TestParseEnabled(t *testing.T) {
	cases := map[string]bool{"": true, "true": true, "1": true, "false": false, "0": false, "garbage": true}
	for raw, want := range cases {
		if got := parseEnabled(raw); got != want {
			t.Fatalf("parseEnabled(%q)=%v want %v", raw, got, want)
		}
	}
}

// The codec cache must return a stable result (including the negative result) so
// the hot path does not re-resolve per request. MUTATION: never store the entry
// and the second lookup would re-run ForModel (still correct here, but the cache
// existence is the guard) — assert the cached value is identical.
func TestCodecForModel_Caches(t *testing.T) {
	c1, ok1 := codecForModel("gpt-4o")
	c2, ok2 := codecForModel("gpt-4o")
	if !ok1 || !ok2 || c1 != c2 {
		t.Fatalf("codec cache not stable: ok1=%v ok2=%v same=%v", ok1, ok2, c1 == c2)
	}
	if _, ok := codecForModel(""); ok {
		t.Fatal("empty model must not resolve a codec")
	}
}
