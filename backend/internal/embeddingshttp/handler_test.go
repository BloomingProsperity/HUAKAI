package embeddingshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestEmbeddingsHandler_SuccessSettlesPromptTokensAndForwardsPassthrough(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":7,"total_tokens":7}}`,
	})

	rec := env.invoke(t, `{"model":"embed-public","input":["alpha beta","gamma"],"encoding_format":"float","user":"u1"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := env.transport.path; got != "/v1/embeddings" {
		t.Fatalf("upstream path=%q want /v1/embeddings", got)
	}
	if got := env.transport.auth; got != "Bearer sk-test" {
		t.Fatalf("Authorization=%q want provider credential", got)
	}
	if !strings.Contains(env.transport.body, `"encoding_format":"float"`) {
		t.Fatalf("passthrough body lost client option: %s", env.transport.body)
	}
	if !strings.Contains(rec.Body.String(), `"embedding":[0.1,0.2]`) {
		t.Fatalf("response body was not upstream embeddings JSON: %s", rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0", got)
	}
	settle := env.settler.settles[0]
	if settle.Draft.TokensInput != 7 || settle.Draft.TokensOutput != 0 {
		t.Fatalf("settled tokens input/output=%d/%d want 7/0", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
	if !settle.ActualCost.Equal(decimal.RequireFromString("0.007")) {
		t.Fatalf("ActualCost=%s want 0.007", settle.ActualCost)
	}
	if settle.RequestedModel != "embed-public" || settle.UpstreamModel != "text-embedding-3-small" {
		t.Fatalf("model chain requested/upstream=%q/%q", settle.RequestedModel, settle.UpstreamModel)
	}
}

func TestEmbeddingsHandler_Upstream5xxAbortsReservedClaim(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"upstream down"}}`,
	})

	rec := env.invoke(t, `{"model":"embed-public","input":"bill me only on success"}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("status=%d body=%s want normalized upstream error", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on upstream failure", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on upstream failure", got)
	}
	if env.settler.aborts[0].reason == "" {
		t.Fatalf("abort reason must be recorded")
	}
}

func TestEmbeddingsHandler_EmptyInputReturns400BeforeReserve(t *testing.T) {
	cases := []string{
		`{"model":"embed-public","input":[]}`,
		`{"model":"embed-public","input":""}`,
	}
	for _, body := range cases {
		env := newEmbeddingsTestEnv(t, upstreamResponse{status: http.StatusOK, body: `{}`})

		rec := env.invoke(t, body)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s want 400", body, rec.Code, rec.Body.String())
		}
		if got := len(env.claims.reserves); got != 0 {
			t.Fatalf("body=%s reserve calls=%d want 0", body, got)
		}
		if env.transport.called {
			t.Fatalf("body=%s should not reach upstream", body)
		}
	}
}

type embeddingsTestEnv struct {
	deps      Deps
	claims    *recordingClaimGate
	settler   *recordingSettler
	transport *recordingRoundTripper
}

type upstreamResponse struct {
	status int
	body   string
}

func newEmbeddingsTestEnv(t *testing.T, resp upstreamResponse) *embeddingsTestEnv {
	t.Helper()
	claims := &recordingClaimGate{nextClaimID: 9001}
	settler := &recordingSettler{}
	rt := &recordingRoundTripper{resp: resp}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	return &embeddingsTestEnv{
		claims:    claims,
		settler:   settler,
		transport: rt,
		deps: Deps{
			Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
			Registry:              registryStub{},
			Router:                routerStub{},
			ClaimGate:             claims,
			RateTables:            rateTableStub{},
			Selector:              selectorStub{},
			CredentialVault:       vaultStub{},
			Dispatcher:            &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf},
			Settler:               settler,
			BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
			BillingPolicyVersion:  "test-policy",
			RequestClass:          "standard",
		},
	}
}

func (e *embeddingsTestEnv) invoke(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewEmbeddingsHandler(e.deps))
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *embeddingsTestEnv) assertNoHangingClaims(t *testing.T) {
	t.Helper()
	closed := map[int64]string{}
	for _, req := range e.settler.settles {
		closed[req.ClaimID] = "settled"
	}
	for _, req := range e.settler.aborts {
		if prior := closed[req.claimID]; prior != "" {
			t.Fatalf("claim %d closed twice: %s and aborted", req.claimID, prior)
		}
		closed[req.claimID] = "aborted"
	}
	for _, req := range e.claims.reserves {
		if got := closed[req.claimID]; got == "" {
			t.Fatalf("reserved claim %d was not settled or aborted", req.claimID)
		}
	}
}

type authStub struct {
	ident auth.Identity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.ident, s.err
}

type registryStub struct{}

func (registryStub) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      "embed-public",
		CanonicalModelID: "embedding/canonical",
		ProviderModelID:  "text-embedding-3-small",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type routerStub struct{}

func (routerStub) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index:           0,
			PoolGroupID:     101,
			Reason:          "primary",
			UpstreamModelID: "text-embedding-3-small",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:test",
	}, nil
}

type recordingClaimGate struct {
	nextClaimID int64
	reserves    []reservedClaim
}

type reservedClaim struct {
	claimID int64
	req     billing.ReserveRequest
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.reserves = append(g.reserves, reservedClaim{claimID: g.nextClaimID, req: req})
	return &billing.ReserveResult{ClaimID: g.nextClaimID}, nil
}

type rateTableStub struct{}

func (rateTableStub) GetRateTable(context.Context, string) (billing.RateTable, error) {
	return billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"text-embedding-3-small":{"input_micro_usd":1000},"default":{"input_micro_usd":1000}}}`),
	}, nil
}

func (rateTableStub) GetRateTableSnapshot(context.Context, int64) (billing.RateTable, error) {
	return billing.RateTable{}, billing.ErrRateTableNotFound
}

func (rateTableStub) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
	return nil, nil
}

type selectorStub struct{}

func (selectorStub) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{
		AccountID:         44,
		AcquisitionToken:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RoutingReasonJSON: []byte(`{"reason":"test"}`),
	}, nil
}

type vaultStub struct{}

func (vaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		AccountID: 44,
		TenantID:  7,
		Platform:  "openai",
	}, nil
}

type recordingSettler struct {
	settles []billing.SettleRequest
	aborts  []abortCall
}

type abortCall struct {
	tenantID int64
	claimID  int64
	reason   string
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settles = append(s.settles, req)
	return &billing.SettleResult{}, nil
}

func (s *recordingSettler) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.aborts = append(s.aborts, abortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	return nil
}

func (s *recordingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *recordingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

type recordingRoundTripper struct {
	mu     sync.Mutex
	resp   upstreamResponse
	called bool
	path   string
	auth   string
	body   string
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	raw, _ := io.ReadAll(req.Body)
	rt.called = true
	rt.path = req.URL.Path
	rt.auth = req.Header.Get("Authorization")
	rt.body = string(raw)
	status := rt.resp.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(rt.resp.body)),
		Request:    req,
	}, nil
}
