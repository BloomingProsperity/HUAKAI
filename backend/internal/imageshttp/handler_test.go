package imageshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestImagesHandler_PerImageCostUsesSizeQualityAndNExactlyOnce(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/a.png"},{"url":"https://img.test/b.png"}]}`,
	})
	env.rateTable.raw = imageRateTableFixture(2, 10, 1000)

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"paint a precise ledger","size":"1024x1792","quality":"hd","n":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := env.transport.path; got != "/v1/images/generations" {
		t.Fatalf("upstream path=%q want /v1/images/generations", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	want := decimal.RequireFromString("0.006")
	assertImagesDecimal(t, "reserve PredictedCost", env.claims.reserves[0].req.PredictedCost, want)
	assertImagesDecimal(t, "settle ActualCost", env.settler.settles[0].ActualCost, want)
	if env.settler.settles[0].Draft.TokensInput != 0 || env.settler.settles[0].Draft.TokensOutput != 0 {
		t.Fatalf("per-image settle tokens input/output=%d/%d want 0/0",
			env.settler.settles[0].Draft.TokensInput, env.settler.settles[0].Draft.TokensOutput)
	}
}

func TestImagesHandler_TokenImageSettlesReportedUsageNotReserveEstimate(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}],"usage":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"image_tokens":3}}}`,
	})

	rec := env.invoke(t, `{"model":"gpt-image-1","prompt":"transparent icon","size":"1024x1024","background":"transparent"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	assertImagesDecimal(t, "actual usage cost", env.settler.settles[0].ActualCost, decimal.RequireFromString("0.029"))
	if env.claims.reserves[0].req.PredictedCost.Equal(env.settler.settles[0].ActualCost) {
		t.Fatalf("token-image fixture is non-discriminating: reserve=%s actual=%s",
			env.claims.reserves[0].req.PredictedCost, env.settler.settles[0].ActualCost)
	}
	settle := env.settler.settles[0]
	if settle.Draft.TokensInput != 7 || settle.Draft.TokensOutput != 11 {
		t.Fatalf("settled tokens input/output=%d/%d want 7/11", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
	if !strings.Contains(env.transport.body, `"background":"transparent"`) {
		t.Fatalf("raw passthrough body lost gpt-image field: %s", env.transport.body)
	}
}

func TestImagesHandler_TokenImageMissingUsageAbortsWithoutZeroSettle(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}]}`,
	})

	rec := env.invoke(t, `{"model":"gpt-image-1","prompt":"no usage","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 when token-image usage missing", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 when token-image usage missing", got)
	}
	if env.settler.aborts[0].reason != "usage_missing" {
		t.Fatalf("abort reason=%q want usage_missing", env.settler.aborts[0].reason)
	}
}

func TestImagesHandler_ModelCatalogValidationHappensBeforeReserve(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "dall-e3 n too high", body: `{"model":"dall-e-3","prompt":"x","size":"1024x1024","n":2}`, wantCode: http.StatusBadRequest},
		{name: "dall-e2 n max ok", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":10}`, wantCode: http.StatusOK},
		{name: "dall-e2 n too high", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":11}`, wantCode: http.StatusBadRequest},
		{name: "dall-e3 size rejected", body: `{"model":"dall-e-3","prompt":"x","size":"512x512","n":1}`, wantCode: http.StatusBadRequest},
		{name: "dall-e2 size ok", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":1}`, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})

			rec := env.invoke(t, tt.body)

			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if got := len(env.claims.reserves); got != 0 {
					t.Fatalf("reserve calls=%d want 0 for pre-reserve validation failure", got)
				}
				if env.transport.called {
					t.Fatal("upstream called before rejecting invalid request")
				}
				return
			}
			env.assertNoHangingClaims(t)
		})
	}
}

func TestImagesHandler_PromptRulesAreEndpointSpecificAndCatalogDriven(t *testing.T) {
	longPrompt := strings.Repeat("a", 1001)
	tests := []struct {
		name     string
		endpoint imageEndpoint
		body     string
		wantCode int
	}{
		{name: "generation empty prompt", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-2","prompt":"","size":"512x512"}`, wantCode: http.StatusBadRequest},
		{name: "variation no prompt ok", endpoint: imageEndpointVariations, body: `{"model":"dall-e-2","image_url":"https://img.test/source.png","size":"512x512"}`, wantCode: http.StatusOK},
		{name: "dall-e2 prompt over max", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-2","prompt":"` + longPrompt + `","size":"512x512"}`, wantCode: http.StatusBadRequest},
		{name: "dall-e3 prompt over dall-e2 max ok", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-3","prompt":"` + longPrompt + `","size":"1024x1024"}`, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newImagesTestEnv(t, tt.endpoint, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})

			rec := env.invoke(t, tt.body)

			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if got := len(env.claims.reserves); got != 0 {
					t.Fatalf("reserve calls=%d want 0 for invalid prompt", got)
				}
				return
			}
			env.assertNoHangingClaims(t)
		})
	}
}

func TestImagesHandler_Upstream5xxAbortsReservedClaim(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"upstream down"}}`,
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"bill only on success","size":"512x512"}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("status=%d body=%s want normalized upstream failure", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on upstream failure", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on upstream failure", got)
	}
}

func TestImagesHandler_Upstream2xxEmptyBodyAbortsReservedClaim(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   ``,
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"empty upstream body","size":"512x512"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on empty upstream body", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on empty upstream body", got)
	}
}

func TestImagesHandler_SettleErrorReturns500WithoutAbort(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`,
	})
	env.settler.settleErr = errors.New("settle backend down")

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"settle fails","size":"512x512"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s want 500", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 on settle backend error", got)
	}
}

func TestImagesHandler_GroupRatioDiscountsReserveAndSettle(t *testing.T) {
	base := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})
	baseRec := base.invoke(t, `{"model":"dall-e-2","prompt":"ratio","size":"512x512","n":2}`)
	if baseRec.Code != http.StatusOK {
		t.Fatalf("baseline status=%d body=%s want 200", baseRec.Code, baseRec.Body.String())
	}
	discounted := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})
	discounted.deps.PricingRatioResolver = &pricingRatioResolverStub{ratio: decimal.RequireFromString("0.8")}
	discountedRec := discounted.invoke(t, `{"model":"dall-e-2","prompt":"ratio","size":"512x512","n":2}`)
	if discountedRec.Code != http.StatusOK {
		t.Fatalf("discounted status=%d body=%s want 200", discountedRec.Code, discountedRec.Body.String())
	}

	ratio := decimal.RequireFromString("0.8")
	assertImagesDecimal(t, "discounted reserve", discounted.claims.reserves[0].req.PredictedCost, base.claims.reserves[0].req.PredictedCost.Mul(ratio))
	assertImagesDecimal(t, "discounted settle", discounted.settler.settles[0].ActualCost, base.settler.settles[0].ActualCost.Mul(ratio))
	if !strings.Contains(discounted.settler.settles[0].Draft.CostSnapshot, "group_ratio=0.8") {
		t.Fatalf("CostSnapshot=%q want group_ratio=0.8", discounted.settler.settles[0].Draft.CostSnapshot)
	}
}

func TestImagesHandler_ResponseIsUpstreamBytesWithAllowedHeaders(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"b64_json":"abc"}]}`,
		headers: http.Header{
			"Content-Type":         []string{"application/json"},
			"X-Request-Id":         []string{"upstream-req"},
			"Openai-Processing-Ms": []string{"123"},
			"X-Internal-Secret":    []string{"must-not-pass"},
			"Openai-Organization":  []string{"must-not-pass"},
			"Openai-Version":       []string{"2026-01-01"},
		},
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"headers","size":"512x512","response_format":"b64_json"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"created":1,"data":[{"b64_json":"abc"}]}` {
		t.Fatalf("body=%s want exact upstream bytes", got)
	}
	if !headerContains(rec.Header(), "X-Request-Id", "upstream-req") || rec.Header().Get("Openai-Processing-Ms") != "123" {
		t.Fatalf("allowed headers missing: %v", rec.Header())
	}
	if got := rec.Header().Get("X-Internal-Secret"); got != "" {
		t.Fatalf("blocked header propagated: %q", got)
	}
}

type imagesTestEnv struct {
	deps      Deps
	claims    *recordingClaimGate
	settler   *recordingSettler
	transport *recordingRoundTripper
	rateTable *rateTableStub
	endpoint  imageEndpoint
}

type upstreamResponse struct {
	status  int
	body    string
	headers http.Header
}

func newImagesTestEnv(t *testing.T, endpoint imageEndpoint, resp upstreamResponse) *imagesTestEnv {
	t.Helper()
	claims := &recordingClaimGate{nextClaimID: 9101}
	settler := &recordingSettler{}
	rt := &recordingRoundTripper{resp: resp}
	rates := &rateTableStub{raw: imageRateTableFixture(1, 10, 1000)}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	deps := Deps{
		Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
		Registry:              registryStub{},
		Router:                routerStub{},
		ClaimGate:             claims,
		RateTables:            rates,
		Selector:              selectorStub{},
		CredentialVault:       vaultStub{},
		Dispatcher:            &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf},
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	return &imagesTestEnv{deps: deps, claims: claims, settler: settler, transport: rt, rateTable: rates, endpoint: endpoint}
}

func (e *imagesTestEnv) invoke(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	var h http.HandlerFunc
	switch e.endpoint {
	case imageEndpointEdits:
		h = NewEditsHandler(e.deps)
	case imageEndpointVariations:
		h = NewVariationsHandler(e.deps)
	default:
		h = NewGenerationsHandler(e.deps)
	}
	req := httptest.NewRequest(http.MethodPost, e.endpoint.Path(), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequestID(h).ServeHTTP(rec, req)
	return rec
}

func (e *imagesTestEnv) assertNoHangingClaims(t *testing.T) {
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

func (registryStub) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "image/" + model,
		ProviderModelID:  model,
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"image_output"},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type routerStub struct{}

func (routerStub) Plan(_ context.Context, in router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index:           0,
			PoolGroupID:     101,
			Reason:          "primary",
			UpstreamModelID: in.Model.ProviderModelID,
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

type rateTableStub struct {
	raw json.RawMessage
}

func (s *rateTableStub) GetRateTable(context.Context, string) (billing.RateTable, error) {
	return billing.RateTable{Version: "test-policy", PricingData: s.raw}, nil
}

func (s *rateTableStub) GetRateTableSnapshot(context.Context, int64) (billing.RateTable, error) {
	return billing.RateTable{}, billing.ErrRateTableNotFound
}

func (s *rateTableStub) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
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

type pricingRatioResolverStub struct {
	ratio decimal.Decimal
}

func (s *pricingRatioResolverStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s == nil || s.ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return s.ratio, nil
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
	headers := rt.resp.headers.Clone()
	if headers == nil {
		headers = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(rt.resp.body)),
		Request:    req,
	}, nil
}

func imageRateTableFixture(dallE3Max, dallE2Max int, promptMaxDallE2 int) json.RawMessage {
	return json.RawMessage(`{
		"providers": {
			"openai": {
				"models": {
					"dall-e-3": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "1000",
						"image_size_multipliers": {"1024x1024": "1", "1024x1792": "2.0"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.25", "hd@1024x1792": "1.5"},
						"image_amount_range": {"min": 1, "max": ` + intString(dallE3Max) + `},
						"image_prompt_max_chars": 4000
					},
					"dall-e-2": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "500",
						"image_size_multipliers": {"256x256": "0.5", "512x512": "1", "1024x1024": "2"},
						"image_quality_multipliers": {"standard": "1"},
						"image_amount_range": {"min": 1, "max": ` + intString(dallE2Max) + `},
						"image_prompt_max_chars": ` + intString(promptMaxDallE2) + `
					},
					"gpt-image-1": {
						"pricing_scheme": "token_image",
						"input_micro_usd": "1000",
						"output_micro_usd": "2000",
						"image_output_token_upper_bound": {"1024x1024": 100},
						"image_size_multipliers": {"1024x1024": "1"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.2"},
						"image_amount_range": {"min": 1, "max": 4},
						"image_prompt_max_chars": 4000
					}
				}
			}
		}
	}`)
}

func intString(v int) string {
	return decimal.NewFromInt(int64(v)).String()
}

func assertImagesDecimal(t *testing.T, field string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s=%s want %s", field, got, want)
	}
}

func headerContains(h http.Header, key, want string) bool {
	for _, got := range h.Values(key) {
		if got == want {
			return true
		}
	}
	return false
}
