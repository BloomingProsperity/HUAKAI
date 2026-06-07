package apikeymodelallow

import "testing"

func TestModelAllowlistMatcher(t *testing.T) {
	// MUTATION: returning true for a non-empty allowlist miss makes the
	// gpt-3.5 case go red; trimming/case regressions make the mixed-case case red.
	allowGPT4AndClaude := "gpt-4o,claude-3"
	mixedCase := " GPT-4O , Claude-3 "
	empty := ""
	blank := " , "
	tests := []struct {
		name           string
		allowlist      *string
		requestedModel string
		want           bool
	}{
		{name: "nil_allowlist_allows_any_model", allowlist: nil, requestedModel: "gpt-3.5", want: true},
		{name: "empty_allowlist_allows_any_model", allowlist: &empty, requestedModel: "gpt-3.5", want: true},
		{name: "blank_allowlist_allows_any_model", allowlist: &blank, requestedModel: "gpt-3.5", want: true},
		{name: "listed_model_allowed", allowlist: &allowGPT4AndClaude, requestedModel: "gpt-4o", want: true},
		{name: "unlisted_model_forbidden_fail_closed", allowlist: &allowGPT4AndClaude, requestedModel: "gpt-3.5", want: false},
		{name: "case_and_whitespace_normalized", allowlist: &mixedCase, requestedModel: " claude-3 ", want: true},
		{name: "blank_requested_model_denied_when_restricted", allowlist: &allowGPT4AndClaude, requestedModel: " ", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllowsCSV(tc.allowlist, tc.requestedModel); got != tc.want {
				t.Fatalf("AllowsCSV(%v, %q)=%v want %v", valueOf(tc.allowlist), tc.requestedModel, got, tc.want)
			}
		})
	}
}

func valueOf(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}
