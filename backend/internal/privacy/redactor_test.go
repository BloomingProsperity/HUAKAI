package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
