// Slice 2 (N+5b 2026-05-01) unit tests for the chat handler error
// mapping + the body-field-removed transition reject. Happy path runs
// only in the smoke test (cmd/gateway/smoke_test.go) because it
// requires a real *gateway.StreamForwarder + real upstream wire bytes.

package gatewayhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

type stubAuth struct {
	identity auth.Identity
	err      error
}

func (s stubAuth) Resolve(_ context.Context, _ *http.Request) (auth.Identity, error) {
	return s.identity, s.err
}

type stubRegistry struct {
	resolved registry.Resolved
	err      error
}

func (s stubRegistry) ResolveModel(_ context.Context, _ string, _ int64) (registry.Resolved, error) {
	return s.resolved, s.err
}

type stubRouter struct {
	plan router.RoutePlan
	err  error
}

func (s stubRouter) Plan(_ context.Context, _ router.PlanInput) (router.RoutePlan, error) {
	return s.plan, s.err
}

type stubClaimGate struct{}

func (stubClaimGate) Reserve(_ context.Context, _ billing.ReserveRequest) (*billing.ReserveResult, error) {
	return &billing.ReserveResult{ClaimID: 999}, nil
}

type stubSelector struct{}

func (stubSelector) Select(_ context.Context, _ pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{AccountID: 1}, nil
}

type stubSettler struct {
	abortCalls int
}

func (s *stubSettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}
func (s *stubSettler) Abort(_ context.Context, _, _ int64, _ string) error {
	s.abortCalls++
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validIdentity returns a non-zero auth.Identity for tests that need to
// pass the Auth gate without exercising it.
func validIdentity() auth.Identity {
	return auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3}
}

// invokeHandler runs the handler with the given dependencies and request
// body, returning the recorder for assertion.
func invokeHandler(t *testing.T, deps ChatHandlerDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// minimalDeps wires every dependency to a passing stub. Individual
// tests override one stub at a time so the other layers stay quiet.
//
// Forwarder is non-nil but empty: the unit tests in this file all exit
// before Forward() is called (auth fail / registry fail / router fail /
// body field disallowed / nil-guard / etc.). Real Forwarder behavior is
// covered by cmd/gateway/smoke_test.go.
func minimalDeps() ChatHandlerDeps {
	return ChatHandlerDeps{
		Auth:      stubAuth{identity: validIdentity()},
		Registry:  stubRegistry{resolved: registry.Resolved{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}}},
		Router:    stubRouter{plan: router.RoutePlan{Attempts: []router.AttemptPlan{{PoolGroupID: 42}}, SnapshotVersion: "registry:7:1;router:v0.1-phase-c"}},
		ClaimGate: stubClaimGate{},
		Selector:  stubSelector{},
		// 真出站链路 N+5b 后置依赖（dispatcher / vault）：测试占位，让
		// nil-guard 通过；具体上游 Dispatch 不会被这些 stub 测试触及，因为
		// 这一组测试聚焦于 handler 入口校验与计费 / claim 路径，没有真正
		// 进入 forwarder.Forward。
		CredentialVault:      provider.NewStaticVault(),
		Dispatcher:           &gateway.UpstreamDispatcher{},
		Forwarder:            &gateway.StreamForwarder{},
		Settler:              &stubSettler{},
		BillingPolicyVersion: "test-policy",
		RequestClass:         "default",
	}
}

func validBody() string {
	return `{"model":"claude-opus-4-7","stream":true,"messages":[{"role":"user","content":"hi"}]}`
}

// ---------------------------------------------------------------------------
// Tests — N+5b synthesized plan §Test plan (10 cases)
// ---------------------------------------------------------------------------

// 1. NilRegistry → 503 (boot-time misconfig fail-closed; codex synthesis nil-dep).
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

// 2. NilRouter → 503.
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

// 3. RejectsBodyPoolGroupID (positive) → 400 body_field_disallowed.
//
// Synthesized plan §D2 — the gateway no longer accepts client-side pool
// selection. Detection uses raw-JSON pre-parse so explicit-zero is also
// caught (see #4 below).
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

// 4. RejectsBodyPoolGroupIDZero — pointer-only detection would have
// failed here because Go json.Unmarshal lowers null/0 onto the same
// zero value as "field absent". Raw-key detection catches it.
func TestHandler_RejectsBodyPoolGroupIDZero(t *testing.T) {
	d := minimalDeps()
	rec := invokeHandler(t, d, `{"model":"claude-opus-4-7","stream":true,"messages":[],"pool_group_id":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 (zero value still must be rejected)", rec.Code)
	}
}

// 5. RegistryUnknown → uniform 404 model_not_available.
//
// Per codex N+5b P1 pass2 finding (2026-05-01): the response carries NO
// distinguishing header — unknown / disabled / no-access must all look
// identical to clients to defeat alias enumeration.
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

// 6. RegistryDisabled → 404 (indistinguishable from RegistryUnknown).
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

// 7. RegistryNoAccess → 404 (indistinguishable from RegistryUnknown).
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

// 8. RegistryBackend → 503 registry_backend_error. Distinct status
// because a transient infra outage is observably different from "model
// not available" — clients can retry, ops can correlate. No audit
// header (server-side log only).
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

// assertNoAuditHeader asserts the response has NO X-Huakai-Audit-Reason
// header. Per codex N+5b P1 pass2 finding 2026-05-01: the audit reason
// MUST stay server-side; leaking it via response header lets attackers
// distinguish error subclasses that the uniform 404 was meant to hide.
func assertNoAuditHeader(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("X-Huakai-Audit-Reason"); got != "" {
		t.Fatalf("X-Huakai-Audit-Reason MUST NOT appear on public response; got %q", got)
	}
}

// 9. NoStream — non-streaming 进入 ClientAdapter；当 dispatcher 尚未提供
// HCSF buffered 能力时显式 503，而不是 silent fallback。
func TestHandler_NoStream(t *testing.T) {
	d := clientAdapterDeps(t)
	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "non_streaming_not_yet_wired") {
		t.Fatalf("body = %q; want non_streaming_not_yet_wired", rec.Body.String())
	}
}

// 10. MissingModel — model field empty → 400.
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

// 11. UnauthorizedAuth — auth.Resolve returns ErrUnauthorized → 401.
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
