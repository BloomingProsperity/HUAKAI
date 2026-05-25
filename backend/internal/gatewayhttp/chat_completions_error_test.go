package gatewayhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

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
