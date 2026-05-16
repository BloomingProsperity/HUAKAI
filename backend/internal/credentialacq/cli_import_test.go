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
