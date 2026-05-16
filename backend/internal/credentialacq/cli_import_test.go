package credentialacq

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func parseCLIImport(input string) ([]acqCandidate, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, errInvalidImportBody
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		return candidatesFromDecoded(decoded)
	}

	lines := strings.Split(trimmed, "\n")
	candidates := make([]acqCandidate, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var one any
		if err := json.Unmarshal([]byte(line), &one); err == nil {
			got, err := candidatesFromDecoded(one)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, got...)
			continue
		}
		candidates = append(candidates, tokenCandidate(line))
	}
	if len(candidates) == 0 {
		return nil, errInvalidImportBody
	}
	return candidates, nil
}

func candidatesFromDecoded(decoded any) ([]acqCandidate, error) {
	switch v := decoded.(type) {
	case map[string]any:
		return []acqCandidate{candidateFromMap(v)}, nil
	case []any:
		out := make([]acqCandidate, 0, len(v))
		for _, item := range v {
			switch typed := item.(type) {
			case map[string]any:
				out = append(out, candidateFromMap(typed))
			case string:
				out = append(out, tokenCandidate(typed))
			default:
				return nil, errInvalidImportBody
			}
		}
		return out, nil
	case string:
		return []acqCandidate{tokenCandidate(v)}, nil
	default:
		return nil, errInvalidImportBody
	}
}

func candidateFromMap(fields map[string]any) acqCandidate {
	vendor := stringField(fields, "vendor", credentialstore.VendorOpenAI)
	mode := stringField(fields, "auth_mode", credentialstore.AuthModeCodexCLIOAuth)
	payload, _ := json.Marshal(fields)
	return acqCandidate{
		Vendor: vendor, AuthMode: mode, Payload: payload,
		RedactedContext: map[string]any{"shape": "json_object"},
	}
}

func tokenCandidate(token string) acqCandidate {
	payload, _ := json.Marshal(map[string]string{"session_token": strings.TrimSpace(token)})
	return acqCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, Payload: payload,
		RedactedContext: map[string]any{"shape": "single_token"},
	}
}

func stringField(fields map[string]any, key, fallback string) string {
	value, ok := fields[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

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
			got, err := parseCLIImport(tc.input)
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
	if _, err := parseCLIImport(" \n\t "); err != errInvalidImportBody {
		t.Fatalf("err=%v want %v", err, errInvalidImportBody)
	}
}
