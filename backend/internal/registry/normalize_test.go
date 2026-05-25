package registry

import "testing"

func TestAliasNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace_only", "   ", ""},
		// Test inputs taken from the verified-current model lineup
		// (docs/process/plans/2026-04-30-n5-model-registry.md Appendix B):
		// claude-opus-4-7 / claude-haiku-4-5 (Anthropic 2026-04-30T10:08Z),
		// gpt-5.4-mini (OpenAI 2026-04-30T10:09Z),
		// gemini-2.5-pro (Google 2026-04-30T10:08Z).
		{"plain", "claude-opus-4-7", "claude-opus-4-7"},
		{"lowercases_uppercase", "GPT-5.4-Mini", "gpt-5.4-mini"},
		{"trims_outer_whitespace", "  claude-haiku-4-5\t", "claude-haiku-4-5"},
		{"collapses_mixed_case", "Claude-Opus-4-7", "claude-opus-4-7"},
		// NFC normalization: precomposed form. We don't generate
		// decomposed sequences in fixtures, but validate the call path.
		{"ascii_unaffected_by_nfc", "gemini-2.5-pro", "gemini-2.5-pro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AliasNormalize(tc.in)
			if got != tc.want {
				t.Fatalf("AliasNormalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
