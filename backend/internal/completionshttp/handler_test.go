package completionshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestCompletionsReserveThenSettle_HappyPath(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"id":"cmpl_1","object":"text_completion","model":"text-davinci-003","choices":[{"text":"world","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
	})

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"hello","max_tokens":8,"temperature":0.2,"vendor_extension":{"keep":true}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if env.dispatcher.lastInput.EndpointPath != "/v1/completions" {
		t.Fatalf("EndpointPath=%q want /v1/completions", env.dispatcher.lastInput.EndpointPath)
	}
	if !strings.Contains(string(env.dispatcher.lastInput.InboundBody), `"vendor_extension":{"keep":true}`) {
		t.Fatalf("dispatcher body lost unknown passthrough field: %s", string(env.dispatcher.lastInput.InboundBody))
	}
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0", got)
	}
	settle := env.settler.settles[0]
	if settle.RequestedModel != "legacy-public" || settle.UpstreamModel != "text-davinci-003" {
		t.Fatalf("model chain requested/upstream=%q/%q", settle.RequestedModel, settle.UpstreamModel)
	}
	if settle.Draft.TokensInput != 3 || settle.Draft.TokensOutput != 5 {
		t.Fatalf("settled tokens input/output=%d/%d want 3/5", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
	if !settle.ActualCost.Equal(decimal.RequireFromString("0.013")) {
		t.Fatalf("ActualCost=%s want 0.013 from 3 input + 5 output tokens", settle.ActualCost)
	}
}

func TestCompletionsUpstreamErrorAborts(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	env.dispatcher.err = errors.New("dial upstream failed")

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"release reservation"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on dispatch failure", got)
	}
	// Mutation: removing Abort after dispatcher error leaves a reserved claim open.
	// This assertion must turn RED if the abort call is skipped.
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on dispatch failure", got)
	}
	if env.settler.aborts[0].reason != "upstream_dispatch_error" {
		t.Fatalf("abort reason=%q want upstream_dispatch_error", env.settler.aborts[0].reason)
	}
}

func TestCompletionsTokenBilling(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"id":"cmpl_usage","object":"text_completion","model":"text-davinci-003","choices":[{"text":"charged output","index":0}],"usage":{"prompt_tokens":2,"completion_tokens":9,"total_tokens":11}}`,
	})

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":["a","b"],"max_tokens":12}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	settle := env.settler.settles[0]
	if settle.Draft.TokensInput != 2 || settle.Draft.TokensOutput != 9 {
		t.Fatalf("tokens input/output=%d/%d want 2/9", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
	// Mutation: billing a flat amount or using prompt_tokens only makes this RED
	// because output_micro_usd=2000 and completion_tokens=9 dominate the total.
	if !settle.ActualCost.Equal(decimal.RequireFromString("0.020")) {
		t.Fatalf("ActualCost=%s want 0.020 from upstream usage prompt+completion tokens", settle.ActualCost)
	}
}

func TestCompletionsInsufficientBalance(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
	})
	env.claims.err = billing.ErrInsufficientBalance

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"too expensive"}`)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d body=%s want 402", rec.Code, rec.Body.String())
	}
	if got := env.dispatcher.calls; got != 0 {
		t.Fatalf("dispatcher calls=%d want 0 when reserve denies balance", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
}

func TestCompletionsValidation(t *testing.T) {
	cases := []string{
		`{"prompt":"missing model"}`,
		`{"model":"legacy-public","prompt":""}`,
		`{"model":"legacy-public","prompt":["valid"," "]}`,
	}
	for _, body := range cases {
		env := newCompletionsTestEnv(upstreamResponse{status: http.StatusOK, body: `{}`})

		rec := env.invokeCompletions(t, body)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s want 400", body, rec.Code, rec.Body.String())
		}
		if got := len(env.claims.reserves); got != 0 {
			t.Fatalf("body=%s reserve calls=%d want 0", body, got)
		}
		if got := env.dispatcher.calls; got != 0 {
			t.Fatalf("body=%s dispatcher calls=%d want 0", body, got)
		}
	}
}

func TestCompletionsStreaming(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"id":"cmpl_stream","object":"text_completion","choices":[{"text":"hel","index":0}]}`,
		`data: {"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6},"choices":[]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	env := newCompletionsTestEnv(upstreamResponse{
		status:      http.StatusOK,
		body:        streamBody,
		contentType: "text/event-stream",
	})

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"stream please","stream":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("Content-Type=%q want text/event-stream", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != streamBody {
		t.Fatalf("stream body mismatch:\n got %q\nwant %q", rec.Body.String(), streamBody)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1 after stream with usage frame", got)
	}
	if !env.settler.settles[0].Stream {
		t.Fatalf("settle Stream=false want true")
	}
	if !env.settler.settles[0].ActualCost.Equal(decimal.RequireFromString("0.010")) {
		t.Fatalf("stream ActualCost=%s want 0.010", env.settler.settles[0].ActualCost)
	}
}

func TestCountTokensPassthroughNoBilling(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"input_tokens":42,"cache_creation_input_tokens":3}`,
	})

	rec := env.invokeCountTokens(t, `{"model":"claude-public","messages":[{"role":"user","content":"hello"}],"system":"be brief"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if env.dispatcher.lastInput.EndpointPath != "/v1/messages/count_tokens" {
		t.Fatalf("EndpointPath=%q want /v1/messages/count_tokens", env.dispatcher.lastInput.EndpointPath)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"input_tokens":42,"cache_creation_input_tokens":3}` {
		t.Fatalf("body=%s want upstream count_tokens JSON verbatim", got)
	}
	// Mutation: charging count_tokens balance makes these RED; this endpoint is a
	// free utility and must not reserve, settle, abort, or touch quota.
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0 for free count_tokens", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 for free count_tokens", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 for free count_tokens", got)
	}
}

func TestCountTokensAuthRequired(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{status: http.StatusOK, body: `{"input_tokens":1}`})
	env.deps.Auth = authStub{err: errors.New("invalid bearer")}

	rec := env.invokeCountTokens(t, `{"model":"claude-public","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if got := env.dispatcher.calls; got != 0 {
		t.Fatalf("dispatcher calls=%d want 0 without auth", got)
	}
}

func TestCountTokensValidation(t *testing.T) {
	cases := []string{
		`{"messages":[{"role":"user","content":"hello"}]}`,
		`{"model":"claude-public","messages":[]}`,
	}
	for _, body := range cases {
		env := newCompletionsTestEnv(upstreamResponse{status: http.StatusOK, body: `{"input_tokens":1}`})

		rec := env.invokeCountTokens(t, body)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s want 400", body, rec.Code, rec.Body.String())
		}
		if got := env.dispatcher.calls; got != 0 {
			t.Fatalf("body=%s dispatcher calls=%d want 0", body, got)
		}
		if got := len(env.claims.reserves); got != 0 {
			t.Fatalf("body=%s reserve calls=%d want 0", body, got)
		}
	}
}

type completionsTestEnv struct {
	deps       Deps
	claims     *recordingClaimGate
	settler    *recordingSettler
	dispatcher *recordingDispatcher
}

type upstreamResponse struct {
	status      int
	body        string
	contentType string
}

func newCompletionsTestEnv(resp upstreamResponse) *completionsTestEnv {
	claims := &recordingClaimGate{nextClaimID: 8101}
	settler := &recordingSettler{}
	dispatcher := &recordingDispatcher{resp: resp}
	deps := Deps{
		Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
		Registry:              registryStub{},
		Router:                routerStub{},
		ClaimGate:             claims,
		RateTables:            rateTableStub{},
		Selector:              selectorStub{},
		CredentialVault:       vaultStub{},
		Dispatcher:            dispatcher,
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	return &completionsTestEnv{deps: deps, claims: claims, settler: settler, dispatcher: dispatcher}
}

func (e *completionsTestEnv) invokeCompletions(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewCompletionsHandler(e.deps))
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *completionsTestEnv) invokeCountTokens(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewCountTokensHandler(e.deps))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type authStub struct {
	ident auth.Identity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.ident, s.err
}

type registryStub struct{}

func (registryStub) ResolveModel(_ context.Context, publicAlias string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      publicAlias,
		CanonicalModelID: "completion/canonical",
		ProviderModelID:  "text-davinci-003",
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
			UpstreamModelID: "text-davinci-003",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:test",
	}, nil
}

type recordingClaimGate struct {
	nextClaimID int64
	reserves    []reservedClaim
	err         error
}

type reservedClaim struct {
	claimID int64
	req     billing.ReserveRequest
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.reserves = append(g.reserves, reservedClaim{claimID: g.nextClaimID, req: req})
	if g.err != nil {
		return nil, g.err
	}
	return &billing.ReserveResult{ClaimID: g.nextClaimID}, nil
}

type rateTableStub struct{}

func (rateTableStub) GetRateTable(context.Context, string) (billing.RateTable, error) {
	return billing.RateTable{
		Version: "test-policy",
		PricingData: json.RawMessage(`{
			"models": {
				"text-davinci-003": {"input_micro_usd":1000, "output_micro_usd":2000},
				"default": {"input_micro_usd":1000, "output_micro_usd":2000}
			}
		}`),
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

type recordingDispatcher struct {
	calls     int
	lastInput gateway.DispatchInput
	resp      upstreamResponse
	err       error
}

func (d *recordingDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls++
	d.lastInput = in
	if d.err != nil {
		return nil, d.err
	}
	status := d.resp.status
	if status == 0 {
		status = http.StatusOK
	}
	contentType := d.resp.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	return &gateway.DispatchResult{
		StatusCode:     status,
		Headers:        http.Header{"Content-Type": []string{contentType}},
		UpstreamReader: strings.NewReader(d.resp.body),
		Close:          func() error { return nil },
	}, nil
}

type recordingSettler struct {
	settles   []billing.SettleRequest
	aborts    []abortCall
	settleErr error
}

type abortCall struct {
	tenantID int64
	claimID  int64
	reason   string
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settles = append(s.settles, req)
	if s.settleErr != nil {
		return nil, s.settleErr
	}
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
