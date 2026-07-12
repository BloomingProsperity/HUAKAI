package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestHandler_NilRegistry(t *testing.T) {
	d := minimalDeps()
	d.Registry = nil
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gateway_not_configured") {
		t.Fatalf("body = %q; want gateway_not_configured", rec.Body.String())
	}
}

func TestHandler_NilRouter(t *testing.T) {
	d := minimalDeps()
	d.Router = nil
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gateway_not_configured") {
		t.Fatalf("body = %q; want gateway_not_configured", rec.Body.String())
	}
}

func TestHandler_RejectsBodyPoolGroupID(t *testing.T) {
	d := minimalDeps()
	rec := invokeHandler(t, d, `{"model":"claude-opus-4-7","stream":true,"messages":[],"pool_group_id":5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "body_field_disallowed") {
		t.Fatalf("body = %q; want body_field_disallowed", rec.Body.String())
	}
}

func TestHandler_RejectsBodyPoolGroupIDZero(t *testing.T) {
	d := minimalDeps()
	rec := invokeHandler(t, d, `{"model":"claude-opus-4-7","stream":true,"messages":[],"pool_group_id":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 (zero value still must be rejected)", rec.Code)
	}
}

func TestValidateChatCompletionsRequestInvalidJSONUsesFixedMessage(t *testing.T) {
	d := minimalDeps()
	rec := invokeHandler(t, d, `{"model":"claude-opus-4-7","stream":true,"messages":[SENSITIVE_JSON_MARKER]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "SENSITIVE_JSON_MARKER") || strings.Contains(body, "invalid character") {
		t.Fatalf("invalid_json response leaked parser detail: %s", body)
	}
	if !strings.Contains(body, "request body is not valid JSON") {
		t.Fatalf("body=%s want fixed invalid_json message", body)
	}
}

func TestReadChatRequestBodyErrorDoesNotLeakReaderError(t *testing.T) {
	const marker = "SENSITIVE_BODY_READ_MARKER"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", errReadCloser{err: errors.New(marker)})
	rec := httptest.NewRecorder()

	if _, ok := validateChatCompletionsRequest(rec, req, req.Context()); ok {
		t.Fatal("validateChatCompletionsRequest ok=true want false")
	}
	body := rec.Body.String()
	if strings.Contains(body, marker) {
		t.Fatalf("body_read_error response leaked marker: %s", body)
	}
	if !strings.Contains(body, "request body could not be read") {
		t.Fatalf("body=%s want fixed body_read_error message", body)
	}
}

func TestValidateChatCompletionsRequestServerRequestIDIgnoresClientHeader(t *testing.T) {
	// 变异:把规范 RequestID 改回 middleware.GetReqID(ctx),会让两个规范 ID
	// 都等于 "dup-1",本测试就会失败。
	first := validateChatRequestThroughRequestIDMiddleware(t, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"first"}]}`, "dup-1")
	second := validateChatRequestThroughRequestIDMiddleware(t, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"second"}]}`, "dup-1")

	if first.RequestID == "" || second.RequestID == "" {
		t.Fatalf("canonical request ids must be populated: first=%q second=%q", first.RequestID, second.RequestID)
	}
	if first.RequestID == "dup-1" || second.RequestID == "dup-1" {
		t.Fatalf("canonical request ids must be server-generated, got first=%q second=%q", first.RequestID, second.RequestID)
	}
	if first.RequestID == second.RequestID {
		t.Fatalf("canonical request ids must differ for separate requests with same client header; both=%q", first.RequestID)
	}
	if first.ClientRequestID != "dup-1" {
		t.Fatalf("first ClientRequestID=%q want dup-1", first.ClientRequestID)
	}
	if second.ClientRequestID != "dup-1" {
		t.Fatalf("second ClientRequestID=%q want dup-1", second.ClientRequestID)
	}
}

func validateChatRequestThroughRequestIDMiddleware(t *testing.T, body, clientRequestID string) chatValidatedRequest {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, clientRequestID)
	rec := httptest.NewRecorder()
	var validated chatValidatedRequest
	var ok bool
	middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validated, ok = validateChatCompletionsRequest(w, r, r.Context())
	})).ServeHTTP(rec, req)
	if !ok {
		t.Fatalf("validateChatCompletionsRequest ok=false status=%d body=%s", rec.Code, rec.Body.String())
	}
	return validated
}

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r errReadCloser) Close() error {
	return nil
}

var _ io.ReadCloser = errReadCloser{}

func TestHandler_RegistryUnknown(t *testing.T) {
	d := minimalDeps()
	d.Registry = stubRegistry{err: registry.ErrUnknownModel}
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model_not_available") {
		t.Fatalf("body = %q; want model_not_available", rec.Body.String())
	}
	assertNoAuditHeader(t, rec)
}

func TestHandler_RegistryDisabled(t *testing.T) {
	d := minimalDeps()
	d.Registry = stubRegistry{err: registry.ErrModelDisabled}
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model_not_available") {
		t.Fatalf("body = %q; want model_not_available", rec.Body.String())
	}
	assertNoAuditHeader(t, rec)
}

func TestHandler_RegistryNoAccess(t *testing.T) {
	d := minimalDeps()
	d.Registry = stubRegistry{err: registry.ErrTenantNoAccess}
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model_not_available") {
		t.Fatalf("body = %q; want model_not_available", rec.Body.String())
	}
	assertNoAuditHeader(t, rec)
}

func TestHandler_RegistryBackend(t *testing.T) {
	d := minimalDeps()
	d.Registry = stubRegistry{err: registry.ErrRegistryBackend}
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "registry_backend_error") {
		t.Fatalf("body = %q; want registry_backend_error", rec.Body.String())
	}
	assertNoAuditHeader(t, rec)
}

func assertNoAuditHeader(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("X-Huakai-Audit-Reason"); got != "" {
		t.Fatalf("X-Huakai-Audit-Reason MUST NOT appear on public response; got %q", got)
	}
}

func TestHandler_MissingModel(t *testing.T) {
	d := minimalDeps()
	rec := invokeHandler(t, d, `{"stream":true,"messages":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_model") {
		t.Fatalf("body = %q; want missing_model", rec.Body.String())
	}
}

func TestHandler_UnauthorizedAuth(t *testing.T) {
	d := minimalDeps()
	d.Auth = stubAuth{err: auth.ErrUnauthorized}
	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("body = %q; want unauthorized", rec.Body.String())
	}
}

// TestConfigureBodyLimits 守护:ConfigureBodyLimits 设定入站请求体上限,且 <=0 保留默认(不清零)。
func TestConfigureBodyLimits(t *testing.T) {
	t.Cleanup(func() { maxRequestBodyBytes = defaultMaxRequestBodyBytes })

	ConfigureBodyLimits(7 << 20)
	if maxRequestBodyBytes != 7<<20 {
		t.Fatalf("ConfigureBodyLimits(7MiB) 后 maxRequestBodyBytes=%d,want %d", maxRequestBodyBytes, int64(7<<20))
	}
	ConfigureBodyLimits(0) // <=0 应保留当前值,不清零
	if maxRequestBodyBytes != 7<<20 {
		t.Fatalf("ConfigureBodyLimits(0) 应保留上次值,got %d", maxRequestBodyBytes)
	}
}

// TestReadChatRequestBodyHonorsConfiguredLimit 守护:入站请求体上限真按 maxRequestBodyBytes 生效,
// 而非旧版硬写 1MiB——这正是修复点(旧 1MiB 会把带图/长上下文请求 413 掉)。
//
// 自证式:同函数对"超限"与"未超限" body 给相反结果。变异检查:把 readChatRequestBody 里的
// maxRequestBodyBytes 改回硬写 1<<20,则 200 字节(远小于 1MiB)会通过,"超限应拒"用例由 false→true(RED)。
func TestReadChatRequestBodyHonorsConfiguredLimit(t *testing.T) {
	t.Cleanup(func() { maxRequestBodyBytes = defaultMaxRequestBodyBytes })
	ConfigureBodyLimits(100) // 100 字节小上限:保持测试轻量 + 可判别

	// 超限:200 字节 > 100 → 应拒(ok=false)。旧硬写 1MiB 会放过 200 字节。
	reqOver := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 200)))
	if _, ok := readChatRequestBody(httptest.NewRecorder(), reqOver, context.Background()); ok {
		t.Fatal("body 超过配置上限应被拒(ok=false)")
	}

	// 未超限:50 字节 < 100 → 应通过(ok=true)。
	reqUnder := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 50)))
	body, ok := readChatRequestBody(httptest.NewRecorder(), reqUnder, context.Background())
	if !ok {
		t.Fatal("body 在配置上限内应通过(ok=true)")
	}
	if len(body) != 50 {
		t.Fatalf("通过的 body len=%d,want 50", len(body))
	}
}
