package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

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

func (s *stubSettler) Abort(_ context.Context, _, _ int64, _, _ string) error {
	s.abortCalls++
	return nil
}

func (s *stubSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

func validIdentity() auth.Identity {
	return auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3}
}

func invokeHandler(t *testing.T, deps ChatHandlerDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func invokeResponsesHandlerPath(t *testing.T, deps ChatHandlerDeps, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewResponsesHandler(deps)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func minimalDeps() ChatHandlerDeps {
	return ChatHandlerDeps{
		Auth:      stubAuth{identity: validIdentity()},
		Registry:  stubRegistry{resolved: registry.Resolved{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}}},
		Router:    stubRouter{plan: router.RoutePlan{Attempts: []router.AttemptPlan{{PoolGroupID: 42}}, SnapshotVersion: "registry:7:1;router:v0.1-phase-c"}},
		ClaimGate: stubClaimGate{},
		Selector:  stubSelector{},
		// 真出站链路占位，让入口校验类测试通过 nil-guard。
		CredentialVault:      provider.NewStaticVault(),
		Dispatcher:           &gateway.UpstreamDispatcher{},
		Forwarder:            &gateway.StreamForwarder{},
		Settler:              &stubSettler{},
		RateTables:           testRateTables("test-policy"),
		BillingPolicyVersion: "test-policy",
		RequestClass:         "default",
	}
}

func testRateTables(version string) *rateTableSourceStub {
	return &rateTableSourceStub{table: billing.RateTable{
		Version:     version,
		PricingData: json.RawMessage(`{"models":{"default":{"input_micro_usd":1,"output_micro_usd":1,"cache_creation_micro_usd":1,"cache_read_micro_usd":1}}}`),
	}}
}

func validBody() string {
	return `{"model":"claude-opus-4-7","stream":true,"messages":[{"role":"user","content":"hi"}]}`
}
