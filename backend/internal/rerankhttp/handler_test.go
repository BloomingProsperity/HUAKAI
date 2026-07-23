package rerankhttp

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

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestRerankSearchUnitsBilling(t *testing.T) {
	cases := []struct {
		name      string
		documents int
		units     int64
		wantCost  string
	}{
		{name: "one_doc", documents: 1, units: 1, wantCost: "0.0025"},
		{name: "one_hundred_docs", documents: 100, units: 1, wantCost: "0.0025"},
		{name: "one_hundred_one_docs", documents: 101, units: 2, wantCost: "0.005"},
		{name: "one_hundred_fifty_docs", documents: 150, units: 2, wantCost: "0.005"},
		{name: "two_hundred_fifty_docs", documents: 250, units: 3, wantCost: "0.0075"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newRerankTestEnv(t)

			rec := env.invoke(t, rerankBody(tc.documents))

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
			}
			if got := len(env.claims.reserves); got != 1 {
				t.Fatalf("reserve calls=%d want 1", got)
			}
			if got := len(env.settler.settles); got != 1 {
				t.Fatalf("settle calls=%d want 1", got)
			}
			wantCost := decimal.RequireFromString(tc.wantCost)
			assertRerankDecimal(t, "reserve PredictedCost", env.claims.reserves[0].req.PredictedCost, wantCost)
			assertRerankDecimal(t, "settle ActualCost", env.settler.settles[0].ActualCost, wantCost)
			assertRerankDecimal(t, "settle Draft.ActualCost", env.settler.settles[0].Draft.ActualCost, wantCost)
			if env.claims.reserves[0].req.EndpointFamily != "rerank" {
				t.Fatalf("reserve EndpointFamily=%q want rerank", env.claims.reserves[0].req.EndpointFamily)
			}
			if env.settler.settles[0].Draft.TokensInput <= 0 || env.settler.settles[0].Draft.TokensOutput != 0 {
				t.Fatalf("Draft token counters input/output=%d/%d，期望保留输入估算且输出为 0",
					env.settler.settles[0].Draft.TokensInput, env.settler.settles[0].Draft.TokensOutput)
			}
			if env.settler.settles[0].Draft.UsageSource != gateway.UsageSourceInferred || env.settler.settles[0].Draft.ConfidenceScore == nil || *env.settler.settles[0].Draft.ConfidenceScore != 0.5 {
				t.Fatalf("usage source/confidence=%s/%v，期望 inferred/0.5", env.settler.settles[0].Draft.UsageSource, env.settler.settles[0].Draft.ConfidenceScore)
			}
			if len(env.settler.aborts) != 0 {
				t.Fatalf("abort calls=%d want 0", len(env.settler.aborts))
			}
			// 变异:按 len(query) token 数或文档数计费, 而非
			// ceil(documents/100), 会让 predicted/actual 成本断言变红。
			// 变异:在 upstream 成功后重算出不同的 actual 成本,
			// 会让 reserve 与 settle 相等的断言变红。
		})
	}
}

func TestRerankReserveThenSettle_HappyPath(t *testing.T) {
	env := newRerankTestEnv(t)
	body := `{"model":"rerank-public","query":"find billing docs","documents":["plain",{"text":"object doc","metadata":{"id":"d2"}}],"top_n":1,"return_documents":true}`
	env.dispatcher.body = `{"results":[{"index":1,"relevance_score":0.93,"document":{"text":"object doc"}}]}`

	rec := env.invoke(t, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"relevance_score":0.93`) {
		t.Fatalf("response body was not upstream rerank JSON: %s", rec.Body.String())
	}
	if got := len(env.dispatcher.calls); got != 1 {
		t.Fatalf("dispatch calls=%d want 1", got)
	}
	call := env.dispatcher.calls[0]
	if call.EndpointPath != "/v1/rerank" {
		t.Fatalf("EndpointPath=%q want /v1/rerank", call.EndpointPath)
	}
	if call.UpstreamModelID != "rerank-upstream" {
		t.Fatalf("UpstreamModelID=%q want rerank-upstream", call.UpstreamModelID)
	}
	if !bytes.Equal(call.InboundBody, []byte(body)) {
		t.Fatalf("InboundBody was not forwarded verbatim: %s", string(call.InboundBody))
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0", got)
	}
	// 变异:把 EndpointPath 改回 /v1/embeddings, 或重新构造请求 body 而非
	// 原样转发 raw JSON, 会让本测试变红。
}

func TestRerankUpstreamErrorAborts(t *testing.T) {
	env := newRerankTestEnv(t)
	env.dispatcher.err = errors.New("upstream dial failed")

	rec := env.invoke(t, rerankBody(1))

	assertRerankErrorCode(t, rec, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError)
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1", got)
	}
	if env.settler.aborts[0].reason != "upstream_dispatch_error" {
		t.Fatalf("abort reason=%q want upstream_dispatch_error", env.settler.aborts[0].reason)
	}
	env.assertNoHangingClaims(t)
	// 变异:在 dispatcher 出错时跳过 Settler.Abort, 会让 claim 一直处于
	// reserved 状态, 从而让本测试变红。
}

func TestRerankInsufficientBalance(t *testing.T) {
	env := newRerankTestEnv(t)
	env.claims.err = billing.ErrInsufficientBalance

	rec := env.invoke(t, rerankBody(1))

	assertRerankErrorCode(t, rec, http.StatusPaymentRequired, clienterr.CodeInsufficientBalance)
	if got := len(env.dispatcher.calls); got != 0 {
		t.Fatalf("dispatch calls=%d want 0", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 before reservation succeeds", got)
	}
	// 变异:在 ClaimGate.Reserve 之前就 dispatch, 或吞掉
	// ErrInsufficientBalance, 会让本测试变红。
}

func TestRerankValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{name: "missing_model", body: `{"query":"q","documents":["d"]}`, code: "missing_model"},
		{name: "empty_query", body: `{"model":"rerank-public","query":"   ","documents":["d"]}`, code: "missing_query"},
		{name: "zero_documents", body: `{"model":"rerank-public","query":"q","documents":[]}`, code: "invalid_documents"},
		{name: "too_many_documents", body: rerankBody(maxRerankDocuments + 1), code: "invalid_documents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newRerankTestEnv(t)

			rec := env.invoke(t, tc.body)

			assertRerankErrorCode(t, rec, http.StatusBadRequest, tc.code)
			if got := len(env.claims.reserves); got != 0 {
				t.Fatalf("reserve calls=%d want 0", got)
			}
			if got := len(env.dispatcher.calls); got != 0 {
				t.Fatalf("dispatch calls=%d want 0", got)
			}
			// 变异:允许空的 query/documents, 或在 reserve 之后才校验,
			// 会让本测试变红。
		})
	}
}

func TestRerankUnknownModelReturnsModelNotAvailableBeforeReserve(t *testing.T) {
	env := newRerankTestEnv(t)
	env.registry.err = registry.ErrUnknownModel

	rec := env.invoke(t, rerankBody(1))

	assertRerankErrorCode(t, rec, http.StatusNotFound, "model_not_available")
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0", got)
	}
	if got := len(env.dispatcher.calls); got != 0 {
		t.Fatalf("dispatch calls=%d want 0", got)
	}
	// 变异:在未注册任何 rerank 模型时 fail-open, 会让本测试变红。
}

func TestRerankTenantScoped(t *testing.T) {
	env := newRerankTestEnv(t)

	rec := env.invoke(t, rerankBody(1))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if env.registry.tenantID != 7 {
		t.Fatalf("registry tenantID=%d want 7", env.registry.tenantID)
	}
	if env.router.input.Context.TenantID != 7 || env.router.input.Context.UserID != 13 || env.router.input.Context.APIKeyID != 11 {
		t.Fatalf("router context=%+v want tenant/user/api key 7/13/11", env.router.input.Context)
	}
	if env.claims.reserves[0].req.TenantID != 7 || env.claims.reserves[0].req.UserID != 13 || env.claims.reserves[0].req.APIKeyID != 11 {
		t.Fatalf("reserve identity=%+v want tenant/user/api key 7/13/11", env.claims.reserves[0].req)
	}
	if env.selector.req.TenantID != 7 || env.selector.req.UserID != 13 || env.selector.req.APIKeyID != 11 {
		t.Fatalf("selector request=%+v want tenant/user/api key 7/13/11", env.selector.req)
	}
	if got := env.selector.req.CapabilityFlags; len(got) != 0 {
		t.Fatalf("选号能力=%v，rerank 不得携带账号级媒体能力门(modality 由模型注册表判)", got)
	}
	if env.vault.tenantID != 7 {
		t.Fatalf("vault tenantID=%d want 7", env.vault.tenantID)
	}
	if env.settler.settles[0].TenantID != 7 || env.settler.settles[0].UserID != 13 || env.settler.settles[0].APIKeyID != 11 {
		t.Fatalf("settle identity=%+v want tenant/user/api key 7/13/11", env.settler.settles[0])
	}
	// 变异:在没有已认证 tenant 身份的情况下去解析 model、pool 账号、
	// credential, 或执行 reserve/settle, 会让本测试变红。
}

func TestRerankAuthRequired(t *testing.T) {
	env := newRerankTestEnv(t)
	env.auth.err = auth.ErrUnauthorized

	rec := env.invoke(t, rerankBody(1))

	assertRerankErrorCode(t, rec, http.StatusUnauthorized, "unauthorized")
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0", got)
	}
	if got := len(env.dispatcher.calls); got != 0 {
		t.Fatalf("dispatch calls=%d want 0", got)
	}
	// 变异:在鉴权之前就校验/reserve, 或把缺失鉴权当成匿名成功,
	// 会让本测试变红。
}

type rerankTestEnv struct {
	deps       Deps
	auth       *rerankAuthStub
	registry   *rerankRegistryStub
	router     *rerankRouterStub
	claims     *rerankClaimGate
	rates      *rerankRateTables
	selector   *rerankSelector
	vault      *rerankVault
	dispatcher *rerankDispatcher
	settler    *rerankSettler
}

func newRerankTestEnv(t *testing.T) *rerankTestEnv {
	t.Helper()
	authn := &rerankAuthStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}}
	reg := &rerankRegistryStub{}
	route := &rerankRouterStub{}
	claims := &rerankClaimGate{nextClaimID: 9001}
	rates := &rerankRateTables{raw: json.RawMessage(`{"models":{"rerank-upstream":{"search_unit_micro_usd":2500},"default":{"search_unit_micro_usd":2500}}}`)}
	selector := &rerankSelector{}
	vault := &rerankVault{}
	dispatcher := &rerankDispatcher{body: `{"results":[{"index":0,"relevance_score":0.99}]}`}
	settler := &rerankSettler{}
	env := &rerankTestEnv{
		auth:       authn,
		registry:   reg,
		router:     route,
		claims:     claims,
		rates:      rates,
		selector:   selector,
		vault:      vault,
		dispatcher: dispatcher,
		settler:    settler,
	}
	env.deps = Deps{
		Auth:                  authn,
		Registry:              reg,
		Router:                route,
		ClaimGate:             claims,
		RateTables:            rates,
		Selector:              selector,
		CredentialVault:       vault,
		Dispatcher:            dispatcher,
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	return env
}

func (e *rerankTestEnv) invoke(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewRerankHandler(e.deps))
	req := httptest.NewRequest(http.MethodPost, "/v1/rerank", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *rerankTestEnv) assertNoHangingClaims(t *testing.T) {
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

func rerankBody(documents int) string {
	var b strings.Builder
	b.WriteString(`{"model":"rerank-public","query":"find relevant","documents":[`)
	for i := 0; i < documents; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", fmt.Sprintf("doc-%d", i))
	}
	b.WriteString(`]}`)
	return b.String()
}

func assertRerankDecimal(t *testing.T, field string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s=%s want %s", field, got, want)
	}
}

func assertRerankErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response was not JSON error: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error code=%q body=%s want %q", body.Error.Code, rec.Body.String(), code)
	}
}

type rerankAuthStub struct {
	ident auth.Identity
	err   error
}

func (s *rerankAuthStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.ident, s.err
}

type rerankRegistryStub struct {
	err      error
	model    string
	tenantID int64
}

func (s *rerankRegistryStub) ResolveModel(_ context.Context, model string, tenantID int64) (registry.Resolved, error) {
	s.model = model
	s.tenantID = tenantID
	if s.err != nil {
		return registry.Resolved{}, s.err
	}
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "rerank/canonical",
		ProviderModelID:  "rerank-upstream",
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"rerank"},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type rerankRouterStub struct {
	input router.PlanInput
	err   error
}

func (s *rerankRouterStub) Plan(_ context.Context, in router.PlanInput) (router.RoutePlan, error) {
	s.input = in
	if s.err != nil {
		return router.RoutePlan{}, s.err
	}
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index:           0,
			PoolGroupID:     101,
			Reason:          "primary",
			UpstreamModelID: "rerank-upstream",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:test",
	}, nil
}

type rerankClaimGate struct {
	nextClaimID int64
	err         error
	reserves    []rerankReserveCall
}

type rerankReserveCall struct {
	claimID int64
	req     billing.ReserveRequest
}

func (g *rerankClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	claimID := g.nextClaimID
	if claimID == 0 {
		claimID = 1
	}
	g.reserves = append(g.reserves, rerankReserveCall{claimID: claimID, req: req})
	if g.err != nil {
		return nil, g.err
	}
	return &billing.ReserveResult{ClaimID: claimID}, nil
}

type rerankRateTables struct {
	raw json.RawMessage
	err error
}

func (s *rerankRateTables) GetRateTable(context.Context, string) (billing.RateTable, error) {
	if s.err != nil {
		return billing.RateTable{}, s.err
	}
	return billing.RateTable{Version: "test-policy", PricingData: s.raw}, nil
}

func (s *rerankRateTables) GetRateTableSnapshot(context.Context, int64) (billing.RateTable, error) {
	return billing.RateTable{}, billing.ErrRateTableNotFound
}

func (s *rerankRateTables) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
	return nil, nil
}

type rerankSelector struct {
	req pool.SelectionRequest
	err error
}

func (s *rerankSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.req = req
	if s.err != nil {
		return nil, s.err
	}
	return &pool.SelectionResult{
		AccountID:         44,
		AcquisitionToken:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RoutingReasonJSON: []byte(`{"reason":"test"}`),
	}, nil
}

type rerankVault struct {
	tenantID  int64
	accountID int64
	err       error
}

func (s *rerankVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	s.tenantID = tenantID
	s.accountID = accountID
	if s.err != nil {
		return provider.Credential{}, provider.AccountInfo{}, s.err
	}
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		AccountID:   accountID,
		TenantID:    tenantID,
		Platform:    "openai",
		AccountType: "api_key",
	}, nil
}

type rerankDispatcher struct {
	calls  []gateway.DispatchInput
	status int
	body   string
	err    error
}

func (d *rerankDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.err != nil {
		return nil, d.err
	}
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &gateway.DispatchResult{
		StatusCode:     status,
		Headers:        http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"up-1"}},
		UpstreamReader: io.NopCloser(strings.NewReader(d.body)),
		Close:          func() error { return nil },
	}, nil
}

type rerankSettler struct {
	settles   []billing.SettleRequest
	aborts    []rerankAbortCall
	settleErr error
}

type rerankRecoveryEnqueuer struct {
	calls int
	event dlq.Event
}

func (q *rerankRecoveryEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.calls++
	q.event = event
	return 1, nil
}

type rerankAbortCall struct {
	tenantID int64
	claimID  int64
	reason   string
}

func (s *rerankSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settles = append(s.settles, req)
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

func (s *rerankSettler) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.aborts = append(s.aborts, rerankAbortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	return nil
}

func (s *rerankSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *rerankSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}
