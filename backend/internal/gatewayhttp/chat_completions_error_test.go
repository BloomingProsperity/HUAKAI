package gatewayhttp

import (
	"context"
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
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
)

// TestWriteJSONErrorProducesValidJSONForControlChars 守护 gateway error writer:
// 即便 code/message 带有控制字节(admin create 时 vendor="\x01" 会把 err.Error()
// 流进 message),也必须发出 RFC 合法的 JSON。旧的手写格式化用 fmt %q,会输出
// 像 \x01 这样的 Go 字面量转义 —— 合法 Go、非法 JSON —— 因此严格的 SDK/proxy/日志
// 解析器会失败。
//
// 变异:在 writeJSONError 中恢复 `fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, ...)`;
// json.Valid 会因 \x01 字节而变 false,且 message 的往返相等性
// 失败(字面量转义无法解码回原始字节)→ 本测试变红。
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

func TestRecordModelCooldownOnUpstream404ScopesAccountAndModel(t *testing.T) {
	t.Parallel()

	rec := &recordingModelRateLimiter{}
	recordModelCooldownOnUpstream404(context.Background(), ChatHandlerDeps{ModelCooldowns: rec}, 7, 101, "upstream-gpt-4o", http.StatusNotFound, "req-404")
	if rec.calls != 1 {
		t.Fatalf("calls=%d, want 1", rec.calls)
	}
	if rec.input.TenantID != 7 || rec.input.ProviderAccountID != 101 || rec.input.ModelKey != "upstream-gpt-4o" {
		t.Fatalf("input scope=(tenant=%d account=%d model=%q), want tenant 7 account 101 model upstream-gpt-4o",
			rec.input.TenantID, rec.input.ProviderAccountID, rec.input.ModelKey)
	}
	if rec.input.StatusCode != http.StatusNotFound || rec.input.UpstreamRequestID != "req-404" {
		t.Fatalf("upstream evidence=(%d,%q), want 404 req-404", rec.input.StatusCode, rec.input.UpstreamRequestID)
	}

	recordModelCooldownOnUpstream404(context.Background(), ChatHandlerDeps{ModelCooldowns: rec}, 7, 101, "upstream-gpt-4o", http.StatusBadRequest, "req-400")
	if rec.calls != 1 {
		t.Fatalf("non-404 status recorded model cooldown; calls=%d want 1", rec.calls)
	}
}

type recordingModelRateLimiter struct {
	calls int
	input rate.ModelCooldownInput
}

func (r *recordingModelRateLimiter) RecordModelRateLimit(_ context.Context, in rate.ModelCooldownInput) error {
	r.calls++
	r.input = in
	return nil
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
