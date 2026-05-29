package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// TestWriteJSONErrorProducesValidJSONForControlChars guards S2-148: the gateway error writer must
// emit RFC-valid JSON even when code/message carry control bytes (an admin create with
// vendor="\x01" flows err.Error() into message). The old hand-formatter used fmt %q, which emits
// Go literal escapes like \x01 — valid Go, invalid JSON — so strict SDK/proxy/log parsers fail.
//
// Mutation check: restore `fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, ...)` in
// writeJSONError; json.Valid goes false on the \x01 byte AND the round-trip message equality
// fails (the literal escape does not decode back to the original bytes) → this test goes red.
func TestWriteJSONErrorProducesValidJSONForControlChars(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	msg := "credentialstore: unknown vendor/auth_mode: vendor=\x01 auth_mode=\"oauth\"\nline\\two"
	writeJSONError(rec, http.StatusBadRequest, "admin_bad_request", msg)

	body := rec.Body.Bytes()
	if !json.Valid(body) {
		t.Fatalf("error body must be valid JSON even with control chars; got %q", body)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error body: %v; body=%q", err, body)
	}
	if parsed.Error.Code != "admin_bad_request" {
		t.Fatalf("code must round-trip; got %q", parsed.Error.Code)
	}
	if parsed.Error.Message != msg {
		t.Fatalf("message must round-trip exactly; want %q got %q", msg, parsed.Error.Message)
	}
}

func TestSignalFromClassification_Suppresses401AuthHealthSignal(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusUnauthorized, nil, []byte("invalid_grant"), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := signalFromClassification(http.StatusUnauthorized, classification); got != "" {
		t.Fatalf("signalFromClassification(401 auth)=%q want empty signal", got)
	}
}

func TestSignalFromClassification_StillEmits403Forbidden(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusForbidden, nil, nil, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := signalFromClassification(http.StatusForbidden, classification); got != channelhealth.SignalForbidden {
		t.Fatalf("signalFromClassification(403)=%q want %q", got, channelhealth.SignalForbidden)
	}
}

func TestChatCompletionsPublicErrorsDoNotUseRawErrorStrings(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`writeJSONError\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`classifiedFailureFromDecision\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`retryableLocalAttemptFailure\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`terminalLocalAttemptFailure\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`Header\(\)\.Set\("X-Huakai-[^"]+",[^\n]*\.Error\(\)\)`),
		regexp.MustCompile(`AbortReason[^\n]*\.Error\(\)`),
		regexp.MustCompile(`ClientMessage[^\n]*\.Error\(\)`),
	}
	files, err := filepath.Glob("chat_completions*.go")
	if err != nil {
		t.Fatalf("glob chat_completions*.go: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, pattern := range patterns {
			if loc := pattern.FindIndex(raw); loc != nil {
				t.Fatalf("%s contains public raw error pattern %q near %q", file, pattern.String(), raw[loc[0]:min(len(raw), loc[1]+80)])
			}
		}
	}
}
