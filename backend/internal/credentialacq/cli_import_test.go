package credentialacq

import (
	"bytes"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestCLIImportParsesMultipleShapes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		count int
		shape string
	}{
		{
			name:  "json object",
			input: `{"vendor":"openai","auth_mode":"codex_cli_oauth","session_token":"session-a"}`,
			count: 1,
			shape: "json_object",
		},
		{
			name:  "json array",
			input: `[{"vendor":"openai","auth_mode":"codex_cli_oauth","session_token":"session-a"},{"vendor":"anthropic","auth_mode":"claude_code","session_token":"session-b"}]`,
			count: 2,
			shape: "json_object",
		},
		{
			name:  "json lines",
			input: "{\"vendor\":\"openai\",\"auth_mode\":\"codex_cli_oauth\",\"session_token\":\"session-a\"}\n{\"vendor\":\"anthropic\",\"auth_mode\":\"claude_code\",\"session_token\":\"session-b\"}",
			count: 2,
			shape: "json_object",
		},
		{
			name:  "single token",
			input: "session-single-value",
			count: 1,
			shape: "single_token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseImportContent(tc.input, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.count {
				t.Fatalf("count=%d want %d", len(got), tc.count)
			}
			if got[0].RedactedContext["shape"] != tc.shape {
				t.Fatalf("shape=%v want %s", got[0].RedactedContext["shape"], tc.shape)
			}
			if !bytes.Contains(got[0].Payload, []byte("session")) {
				t.Fatalf("payload does not carry parsed session material: %s", got[0].Payload)
			}
		})
	}
}

func TestCLIImportRejectsEmptyInput(t *testing.T) {
	if _, err := ParseImportContent(" \n\t ", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth); err != ErrInvalidImportBody {
		t.Fatalf("err=%v want %v", err, ErrInvalidImportBody)
	}
}

// TestCLIImportRejectsMalformedJSONLine guards S2-115: a line that clearly intends structured JSON
// (starts with { or [) but fails to parse must be rejected, not silently stored as a raw session
// token — which previously made imports "succeed" with unusable credential text.
//
// Mutation check: drop the jsonLikeLine branch and each malformed line is accepted as a token
// (err==nil) → the "expect ErrInvalidImportBody" assertions go red. The trailing raw-token case
// proves we did NOT regress legitimate non-JSON token import.
func TestCLIImportRejectsMalformedJSONLine(t *testing.T) {
	malformed := []string{
		`{"session_token":"abc"`,    // missing closing brace
		`[{"session_token":"abc"}`,  // missing closing bracket
		`{"vendor":"openai", oops}`, // unquoted garbage
	}
	for _, in := range malformed {
		if _, err := ParseImportContent(in, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth); err != ErrInvalidImportBody {
			t.Fatalf("malformed JSON-like line %q must be rejected; got err=%v", in, err)
		}
	}
	got, err := ParseImportContent("session-raw-value-xyz", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if err != nil || len(got) != 1 || got[0].RedactedContext["shape"] != "single_token" {
		t.Fatalf("non-JSON raw token must still import as single_token; got=%d err=%v", len(got), err)
	}
}
