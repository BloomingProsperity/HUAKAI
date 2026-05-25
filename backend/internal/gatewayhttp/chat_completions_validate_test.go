package gatewayhttp

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
