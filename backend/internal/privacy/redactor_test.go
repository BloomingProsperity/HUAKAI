package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestATPRIV001001003010RedactorBlocksContentAndToolIO(t *testing.T) {
	ctx := context.Background()
	raw, err := DefaultRedactor().SanitizePayload(ctx, map[string]any{
		"request_id":  "req_priv_001",
		"tenant_id":   7,
		"token_count": 11,
		"prompt":      "PROMPT_SENTINEL_secret user text",
		"completion":  "COMPLETION_SENTINEL_secret output",
		"tool_input":  map[string]any{"q": "PROMPT_SENTINEL_secret"},
		"tool_output": "COMPLETION_SENTINEL_secret",
		"metadata":    map[string]any{"raw_body": "PROMPT_SENTINEL_secret"},
	})
	if !errors.Is(err, ErrUnsafePayload) {
		t.Fatalf("err=%v want ErrUnsafePayload", err)
	}
	if ContainsForbiddenRawData(raw) || bytes.Contains(raw, []byte("PROMPT_SENTINEL")) || bytes.Contains(raw, []byte("COMPLETION_SENTINEL")) {
		t.Fatalf("sanitized payload leaked sentinel: %s", raw)
	}
	if !json.Valid(raw) {
		t.Fatalf("sanitized payload is not JSON: %s", raw)
	}
}

func TestATPRIV001004005006011012LoggerAndPanicScrub(t *testing.T) {
	ctx := context.Background()
	var system bytes.Buffer
	userSink := &captureSink{}
	secSink := &captureSink{}
	logger := NewLogger(DefaultRedactor(), &system, userSink, secSink)

	err := logger.LogSystem(ctx, SystemEvent{
		Severity:   SeverityError,
		Component:  "upstream",
		RequestID:  "req_priv_004",
		ErrorClass: "upstream_error",
		Attrs: map[string]any{
			"details": "raw upstream body PROMPT_SENTINEL_secret Authorization: Bearer sk-secret",
		},
	})
	if !errors.Is(err, ErrUnsafePayload) {
		t.Fatalf("system freeform err=%v want unsafe", err)
	}
	_ = logger.LogUserAction(ctx, UserActionEvent{
		TenantID:    7,
		RequestID:   "req_priv_user",
		EventClass:  "auth_failed",
		ReasonClass: "invalid_password",
		Attrs:       map[string]any{"cookie": "session=PROMPT_SENTINEL_secret"},
	})
	_ = logger.LogSecurity(ctx, SecurityEvent{
		TenantID:   7,
		RequestID:  "req_priv_sec",
		EventClass: "cross_tenant_denied",
		ActorIDRef: "actor_ref",
		Attrs:      map[string]any{"authorization": "Bearer sk-secret"},
	})
	all := append(system.Bytes(), userSink.Bytes()...)
	all = append(all, secSink.Bytes()...)
	for _, forbidden := range []string{"PROMPT_SENTINEL", "sk-secret", "Bearer", "raw upstream body"} {
		if bytes.Contains(all, []byte(forbidden)) {
			t.Fatalf("logger leaked %q in %s", forbidden, all)
		}
	}

	var panicLog bytes.Buffer
	recoverer := Recoverer(NewLogger(DefaultRedactor(), &panicLog, nil, nil))
	handler := recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("PROMPT_SENTINEL_secret panic body")
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"prompt":"PROMPT_SENTINEL_secret"}`))
	req.Header.Set("X-Request-ID", "req_priv_panic")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic status=%d want 500", rec.Code)
	}
	if bytes.Contains(panicLog.Bytes(), []byte("PROMPT_SENTINEL")) {
		t.Fatalf("panic log leaked request data: %s", panicLog.Bytes())
	}
}

func TestATPRIV001002007008013014MiddlewareAndProofPolicy(t *testing.T) {
	if ContentBindingDefaultEnabled() {
		t.Fatal("content binding hash must default off")
	}
	proof := OptInContentProof([]byte("tenant-key"), []byte("nonce"), []byte("PROMPT_SENTINEL_secret"))
	if proof == "" || strings.Contains(proof, "PROMPT_SENTINEL") || !strings.HasPrefix(proof, "proof:") {
		t.Fatalf("bad opt-in proof: %q", proof)
	}
	ctxSeen := false
	handler := Middleware(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := MetadataFromContext(r.Context())
		ctxSeen = ok && meta.Model == "claude-3" && meta.MessageCount == 1 && meta.RawBodyDiscard
		body, _ := ioReadAll(r.Body)
		if !bytes.Contains(body, []byte("PROMPT_SENTINEL")) {
			t.Fatalf("handler transit body missing sentinel")
		}
		_, _ = w.Write([]byte("chunk-1\n"))
		_, _ = w.Write([]byte("chunk-2\n"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"PROMPT_SENTINEL_secret"}]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !ctxSeen {
		t.Fatal("request metadata was not parsed into context")
	}
	if got := rec.Body.String(); got != "chunk-1\nchunk-2\n" {
		t.Fatalf("response changed: %q", got)
	}
	usage := SafePayloadOrBlocked(context.Background(), map[string]any{
		"request_id":       "req_priv_usage",
		"ledger_id":        "ldg_1",
		"requested_model":  "claude-3",
		"input_tokens":     3,
		"output_tokens":    5,
		"cost_microcents":  12,
		"prompt":           "PROMPT_SENTINEL_secret",
		"completion":       "COMPLETION_SENTINEL_secret",
		"tenant_scope_ref": "tenant:abc",
		"redaction_result": RedactionResultClean,
	})
	if ContainsForbiddenRawData(usage) {
		t.Fatalf("usage metadata leaked content: %s", usage)
	}
}

func TestSanitizeErrorUsesTokensAndPrivacySentinels(t *testing.T) {
	ctx := context.Background()
	redactor := DefaultRedactor()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "wrapped unsafe payload", err: fmt.Errorf("audit blocked: %w", ErrUnsafePayload), want: ErrorClassPrivacyGuardHit},
		{name: "timeout token", err: errors.New("upstream timeout while reading"), want: "network_timeout"},
		{name: "timed out phrase", err: errors.New("dial timed out before headers"), want: "network_timeout"},
		{name: "rate limit tokens", err: errors.New("provider rate limit reached"), want: "upstream_rate_limit"},
		{name: "rate limited tokens", err: errors.New("provider rate limited request"), want: "upstream_rate_limit"},
		{name: "forbidden token", err: errors.New("upstream forbidden by policy"), want: "upstream_forbidden"},
		{name: "bad request phrase", err: errors.New("bad request from adapter"), want: "invalid_request"},
		{name: "credential token", err: errors.New("credential lookup failed"), want: "credential_error"},
		{name: "key unavailable phrase", err: errors.New("signing key unavailable"), want: "credential_error"},
		{name: "upstream token", err: errors.New("upstream closed response"), want: "upstream_error"},
		{name: "panic token", err: errors.New("panic recovered"), want: "panic"},
		{name: "pirate limit is not rate limit", err: errors.New("pirate limit marker"), want: "internal_error"},
		{name: "panic substring is not panic", err: errors.New("panicky but recovered"), want: "internal_error"},
		{name: "credential substring is not credential", err: errors.New("credentialed helper returned"), want: "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := redactor.SanitizeError(ctx, tc.err)
			if err != nil {
				t.Fatalf("SanitizeError err=%v", err)
			}
			if got != tc.want {
				t.Fatalf("class=%q want %q", got, tc.want)
			}
		})
	}
}

type captureSink struct {
	buf bytes.Buffer
}

func (s *captureSink) WritePrivacyEvent(_ context.Context, raw []byte) error {
	_, _ = s.buf.Write(raw)
	_ = s.buf.WriteByte('\n')
	return nil
}

func (s *captureSink) Bytes() []byte {
	return s.buf.Bytes()
}

func ioReadAll(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r)
	return buf.Bytes(), err
}

// TestMiddlewareFuncPerRequestLimit 守护 MiddlewareFunc 真按"每请求"返回的上限执行(而非固定单值):
// 同一中间件,大上限路径放过、小上限路径 413。这是按路径解耦缓冲上限的基础——若 MiddlewareFunc 忽略
// 传入函数恒用某一固定上限,则两路径之一的断言 RED。
//
// 自证式:用 80 字节 body 跨两个不同上限(大 1000 / 小 10)各打一次,断言放过 vs 413 相反结果。
func TestMiddlewareFuncPerRequestLimit(t *testing.T) {
	const big = 1000
	const small = 10
	limitFor := func(r *http.Request) int {
		if r.URL.Path == "/big" {
			return big
		}
		return small
	}
	handler := MiddlewareFunc(limitFor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	body := strings.Repeat("x", 80) // 80 字节:< big(放过)、> small(拒)

	recBig := httptest.NewRecorder()
	handler.ServeHTTP(recBig, httptest.NewRequest(http.MethodPost, "/big", strings.NewReader(body)))
	if recBig.Code != http.StatusOK {
		t.Fatalf("/big(上限 %d)应放过 80 字节,got status=%d", big, recBig.Code)
	}

	recSmall := httptest.NewRecorder()
	handler.ServeHTTP(recSmall, httptest.NewRequest(http.MethodPost, "/small", strings.NewReader(body)))
	if recSmall.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("/small(上限 %d)应拒 80 字节(413),got status=%d", small, recSmall.Code)
	}
}
