package tokenestimate

import (
	"strings"
	"testing"
)

// TestEstimateInputTokens_CJKvsLatin_Discriminating proves the heuristic charges
// CJK glyphs at a DIFFERENT per-class weight than latin word-runs — i.e. it is
// vendor-weighted, not a flat count. The two fixtures share an identical
// structure (N units, each separated by a single comma) so the only term that
// differs between them is cjkGlyph*N vs wordRun*N; the comma/separator
// contribution is identical and cancels.
//
// Mutation guard: collapse cjkGlyph and wordRun to the same value (the naive
// flat-count defect this whole package exists to avoid) and the two estimates
// become equal → red. This is self-proving: the structural framing isolates the
// class-weight term exactly.
func TestEstimateInputTokens_CJKvsLatin_Discriminating(t *testing.T) {
	const n = 100
	cjkParts := make([]string, n)
	latinParts := make([]string, n)
	for i := 0; i < n; i++ {
		cjkParts[i] = "文"   // one CJK glyph per unit
		latinParts[i] = "a" // one latin letter (a one-letter word-run) per unit
	}
	cjk := strings.Join(cjkParts, ",")
	latin := strings.Join(latinParts, ",")

	for _, family := range []string{"openai_chat", "anthropic_messages", "gemini_messages"} {
		cjkEst := Estimate([]byte(cjk), family)
		latinEst := Estimate([]byte(latin), family)
		if cjkEst == latinEst {
			t.Fatalf("family %q: CJK estimate (%d) must differ from latin word estimate (%d) — cjkGlyph and wordRun weights collapsed to a flat count?", family, cjkEst, latinEst)
		}
	}
}

// TestEstimateInputTokens_Monotonic proves the estimate never shrinks when the
// body grows, and that an empty body estimates exactly 0.
//
// Mutation guard: if the count is clamped/capped at a constant, the longer-body
// assertion goes red; if the empty short-circuit is removed, the empty case
// returns the floor pad (>0) and goes red.
func TestEstimateInputTokens_Monotonic(t *testing.T) {
	if got := Estimate([]byte(""), "openai_chat"); got != 0 {
		t.Fatalf("empty body must estimate 0, got %d", got)
	}
	if got := Estimate([]byte("   \n\t "), "openai_chat"); got != 0 {
		t.Fatalf("whitespace-only body must estimate 0, got %d", got)
	}

	short := []byte("the quick brown fox")
	long := []byte("the quick brown fox jumps over the lazy dog repeatedly and again and again")
	se := Estimate(short, "openai_chat")
	le := Estimate(long, "openai_chat")
	if le < se {
		t.Fatalf("longer body estimate (%d) must be >= shorter (%d)", le, se)
	}
	if le == se {
		t.Fatalf("a strictly longer body should estimate strictly more here (short=%d long=%d)", se, le)
	}
}

// TestEstimate_VendorWeightsDiffer proves the protocol-family selector actually
// switches weight tables: a CJK-heavy body estimates differently across the
// three vendor classes (Gemini charges CJK cheaper than Anthropic).
//
// Mutation guard: if classForProtocolFamily always returns one class, the
// estimates collapse to equal and this goes red.
func TestEstimate_VendorWeightsDiffer(t *testing.T) {
	body := []byte(strings.Repeat("漢字", 200))
	anthropic := Estimate(body, "anthropic_messages")
	gemini := Estimate(body, "gemini_messages")
	if anthropic == gemini {
		t.Fatalf("CJK-heavy body must estimate differently for anthropic (%d) vs gemini (%d) — vendor weights not selected by family?", anthropic, gemini)
	}
}

// TestEstimate_UnknownFamily_FallsBack proves an unknown protocol family does
// not panic and produces a sane positive estimate (OpenAI fallback table).
func TestEstimate_UnknownFamily_FallsBack(t *testing.T) {
	got := Estimate([]byte("hello world"), "some_unknown_family_xyz")
	want := Estimate([]byte("hello world"), "openai_chat")
	if got != want {
		t.Fatalf("unknown family should fall back to openai table: got %d want %d", got, want)
	}
	if got <= 0 {
		t.Fatalf("non-empty body must estimate > 0, got %d", got)
	}
}
