package embeddingshttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintenttest"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
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
	if got := env.selector.req.CapabilityFlags; len(got) != 0 {
		t.Fatalf("选号能力=%v，embeddings 不得携带账号级媒体能力门(modality 由模型注册表判)", got)
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

func TestEmbeddingsHandler_DisabledTenantStopsBeforeUpstream(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{status: http.StatusOK, body: `{}`})
	env.claims.err = billing.ErrTenantInactive

	rec := env.invoke(t, `{"model":"embed-public","input":"must not dispatch"}`)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"tenant_inactive"`) {
		t.Fatalf("status=%d body=%s want 403 tenant_inactive", rec.Code, rec.Body.String())
	}
	if env.transport.called || len(env.settler.settles) != 0 || len(env.settler.aborts) != 0 {
		t.Fatalf("停用租户仍触发 upstream/settle/abort=%v/%d/%d",
			env.transport.called, len(env.settler.settles), len(env.settler.aborts))
	}
}

func TestEmbeddingsHandler_GroupRatioDiscountsReserveAndActualCost(t *testing.T) {
	body := `{"model":"embed-public","input":["alpha beta","gamma"],"encoding_format":"float","user":"u1"}`
	upstream := upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":7,"total_tokens":7}}`,
	}
	base := newEmbeddingsTestEnv(t, upstream)
	baseRec := base.invoke(t, body)
	if baseRec.Code != http.StatusOK {
		t.Fatalf("baseline status=%d body=%s want 200", baseRec.Code, baseRec.Body.String())
	}
	if len(base.settler.settles) != 1 || len(base.claims.reserves) != 1 {
		t.Fatalf("baseline settles/reserves=%d/%d want 1/1", len(base.settler.settles), len(base.claims.reserves))
	}

	discounted := newEmbeddingsTestEnv(t, upstream)
	discounted.deps.PricingRatioResolver = &pricingRatioResolverStub{ratio: decimal.RequireFromString("0.8")}
	discountedRec := discounted.invoke(t, body)
	if discountedRec.Code != http.StatusOK {
		t.Fatalf("discounted status=%d body=%s want 200", discountedRec.Code, discountedRec.Body.String())
	}
	if len(discounted.settler.settles) != 1 || len(discounted.claims.reserves) != 1 {
		t.Fatalf("discounted settles/reserves=%d/%d want 1/1", len(discounted.settler.settles), len(discounted.claims.reserves))
	}

	ratio := decimal.RequireFromString("0.8")
	wantActual := base.settler.settles[0].ActualCost.Mul(ratio)
	wantPredicted := base.claims.reserves[0].req.PredictedCost.Mul(ratio)
	assertEmbeddingsDecimal(t, "discounted ActualCost", discounted.settler.settles[0].ActualCost, wantActual)
	assertEmbeddingsDecimal(t, "discounted Draft.ActualCost", discounted.settler.settles[0].Draft.ActualCost, wantActual)
	assertEmbeddingsDecimal(t, "discounted reserve PredictedCost", discounted.claims.reserves[0].req.PredictedCost, wantPredicted)
	if discounted.settler.settles[0].ActualCost.Equal(base.settler.settles[0].ActualCost) {
		t.Fatalf("discounted ActualCost equals baseline %s; group ratio was not applied", base.settler.settles[0].ActualCost)
	}
	if discounted.claims.reserves[0].req.PredictedCost.Equal(base.claims.reserves[0].req.PredictedCost) {
		t.Fatalf("discounted PredictedCost equals baseline %s; reserve ratio was not applied", base.claims.reserves[0].req.PredictedCost)
	}
	if !strings.Contains(discounted.settler.settles[0].Draft.CostSnapshot, "group_ratio=0.8") {
		t.Fatalf("CostSnapshot=%q want group_ratio=0.8", discounted.settler.settles[0].Draft.CostSnapshot)
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

// TestEmbeddingsHandler_SettlementIntentFailureStopsBeforeUpstream 守住资金恢复
// 证据写失败时的交付前硬门。变异：删掉 InsertPending 或吞掉其错误，会让
// transport 被调用且状态不再是 503。
func TestEmbeddingsHandler_SettlementIntentFailureStopsBeforeUpstream(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
	})
	env.deps.SettlementIntents = settlementintent.NewPostgresStore(nil)
	env.deps.SettlementIntentEnabled = true

	rec := env.invoke(t, `{"model":"embed-public","input":"must not dispatch"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if env.transport.called {
		t.Fatal("恢复证据写失败后不得调用上游")
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1", got)
	}
	env.assertNoHangingClaims(t)
}

func TestEmbeddingsHandler_SettlementIntentSuccessfulLifecycle(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
	})
	store := &settlementintenttest.Store{}
	env.deps.SettlementIntents = store
	env.deps.SettlementIntentEnabled = true

	rec := env.invoke(t, `{"model":"embed-public","input":"lifecycle"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(store.Events(), "->"); got != "pending->delivering->settled" {
		t.Fatalf("intent lifecycle=%q want pending->delivering->settled", got)
	}
	if created := store.Created(); created.ClaimID != env.claims.reserves[0].claimID || created.TenantID != 7 {
		t.Fatalf("intent identity=%+v 未绑定权威 claim/tenant", created)
	}
}

func TestEmbeddingsDeliveryEvidenceFailureStopsClientDeliveryAndSettlement(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"embedding":[9.9]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
	})
	store := &settlementintenttest.Store{DeliveryError: errors.New("注入交付证据故障")}
	env.deps.SettlementIntents = store
	env.deps.SettlementIntentEnabled = true
	health := &embeddingHealthSpy{}
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})

	rec := env.invoke(t, `{"model":"embed-public","input":"delivery gate"}`)

	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "9.9") {
		t.Fatalf("status/body=%d/%s want 503 且无上游业务体", rec.Code, rec.Body.String())
	}
	if len(env.settler.settles) != 0 || len(env.settler.aborts) != 1 {
		t.Fatalf("settle/abort=%d/%d want 0/1", len(env.settler.settles), len(env.settler.aborts))
	}
	if got := strings.Join(store.Events(), "->"); got != "pending->aborted" {
		t.Fatalf("intent lifecycle=%q want pending->aborted", got)
	}
	for _, signal := range health.signals {
		if signal.Class != channelhealth.SignalSuccess {
			t.Fatalf("本地交付证据故障写入失败健康信号: %+v", health.signals)
		}
	}
	if health.forceCooldowns != 0 {
		t.Fatalf("本地交付证据故障污染账号健康: signals=%+v force_cooldowns=%d",
			health.signals, health.forceCooldowns)
	}
}

type embeddingsTestEnv struct {
	deps      Deps
	claims    *recordingClaimGate
	settler   *recordingSettler
	selector  *selectorStub
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
	selector := &selectorStub{}
	rt := &recordingRoundTripper{resp: resp}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	return &embeddingsTestEnv{
		claims:    claims,
		settler:   settler,
		selector:  selector,
		transport: rt,
		deps: Deps{
			Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
			Registry:              registryStub{},
			Router:                routerStub{},
			ClaimGate:             claims,
			RateTables:            rateTableStub{},
			Selector:              selector,
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
		Capabilities:     []string{"embeddings"},
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

type selectorStub struct {
	req pool.SelectionRequest
}

func (s *selectorStub) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.req = req
	return &pool.SelectionResult{
		AccountID:         44,
		AcquisitionToken:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RoutingReasonJSON: []byte(`{"reason":"test"}`),
	}, nil
}

type vaultStub struct{}

func (vaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		AccountID:   44,
		TenantID:    7,
		Platform:    "openai",
		AccountType: "api_key",
	}, nil
}

type recordingSettler struct {
	settles   []billing.SettleRequest
	aborts    []abortCall
	settleErr error
}

type embeddingsRecoveryEnqueuer struct {
	calls int
	event dlq.Event
	err   error
}

func (q *embeddingsRecoveryEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.calls++
	q.event = event
	return 1, q.err
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

func assertEmbeddingsDecimal(t *testing.T, field string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s=%s want %s", field, got, want)
	}
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

func TestEmbeddingsHandler_SettleErrorKeepsDeliveredResponseAndEnqueuesRecovery(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`,
	})
	env.settler.settleErr = errors.New("settle backend down")
	recovery := &embeddingsRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery

	rec := env.invoke(t, `{"model":"embed-public","input":"settle fails after upstream success"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，完整业务响应已交付，期望 200", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle attempts=%d want 1", got)
	}
	// Settle 出错路径【不能】abort：对一个 Settle 可能已部分提交的 claim 做
	// abort 有 double-close 风险。悬挂的 reserve 改由 claim lease 过期 +
	// 对账 worker 回收。锁住 Owner 已批准的 2026-06-04 决策——在此处加 abort
	// 会让本测试变红。
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 -- settle-error must not double-close", got)
	}
	if recovery.calls != 1 || recovery.event.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("recovery calls/kind=%d/%q，期望 1/%q", recovery.calls, recovery.event.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	payload, err := settlementrecovery.Decode(recovery.event.Payload)
	if err != nil {
		t.Fatalf("decode recovery payload: %v", err)
	}
	if payload.Source != settlementrecovery.SourceEmbeddingsDelivered {
		t.Fatalf("recovery source=%q，期望 %q", payload.Source, settlementrecovery.SourceEmbeddingsDelivered)
	}
}

func TestEmbeddingsHandler_SettleAndRecoveryDoubleFailurePersistsIntent(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`,
	})
	env.settler.settleErr = errors.New("settle backend down")
	recovery := &embeddingsRecoveryEnqueuer{err: errors.New("recovery queue down")}
	env.deps.SettleRecoveryDLQ = recovery
	intentStore := &settlementintenttest.Store{}
	env.deps.SettlementIntents = intentStore
	env.deps.SettlementIntentEnabled = true

	rec := env.invoke(t, `{"model":"embed-public","input":"double fault"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，响应已交付不能反悔", rec.Code, rec.Body.String())
	}
	if got := strings.Join(intentStore.Events(), "->"); got != "pending->delivering->recovery_pending" {
		t.Fatalf("双故障意图生命周期=%q", got)
	}
	raw, failureClass := intentStore.RecoveryEvidence()
	payload, err := settlementrecovery.Decode(raw)
	if err != nil || payload.Source != settlementrecovery.SourceEmbeddingsDelivered || failureClass == "" {
		t.Fatalf("双故障恢复证据 source=%q class=%q err=%v", payload.Source, failureClass, err)
	}
}

// twoAttemptRouter 给 2 个 attempt,AttemptBudget=2,用于验证 dispatch 失败后的换账号重试。
type twoAttemptRouter struct{}

func (twoAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, Reason: "primary", UpstreamModelID: "text-embedding-3-small"},
			{Index: 1, PoolGroupID: 101, Reason: "failover", UpstreamModelID: "text-embedding-3-small"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:retry-test",
	}, nil
}

// failThenSucceedRT: 第 1 次 RoundTrip 网络失败,第 2 次成功。
type failThenSucceedRT struct {
	calls int
	body  string
}

func (rt *failThenSucceedRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	rt.calls++
	if rt.calls == 1 {
		return nil, errors.New("dial tcp 203.0.113.7:443: connect: connection refused")
	}
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(rt.body))}, nil
}

// 守 BUG2:embeddings AttemptBudget=2 时,attempt 1 dispatch 网络失败必须重试 attempt 2。
// Mutation: run() 改回无条件 return → 只 1 次 RoundTrip + 错误响应 → 本测试红。
func TestEmbeddings_AttemptBudgetRetriesAfterDispatchFailure(t *testing.T) {
	claims := &recordingClaimGate{nextClaimID: 9101}
	settler := &recordingSettler{}
	rt := &failThenSucceedRT{body: `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":3,"total_tokens":3}}`}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	deps := Deps{
		Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
		Registry:              registryStub{},
		Router:                twoAttemptRouter{},
		ClaimGate:             claims,
		RateTables:            rateTableStub{},
		Selector:              &selectorStub{},
		CredentialVault:       vaultStub{},
		Dispatcher:            &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf},
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	h := middleware.RequestID(NewEmbeddingsHandler(deps))
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(`{"model":"embed-public","input":"alpha"}`))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rt.calls != 2 {
		t.Fatalf("RoundTrip calls=%d want 2 (attempt1 fails, attempt2 retries)", rt.calls)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 (retry succeeded)", rec.Code, rec.Body.String())
	}
	if got := len(settler.settles); got != 1 {
		t.Fatalf("settles=%d want 1 (only the successful retry settles)", got)
	}
	if got := len(settler.aborts); got != 1 {
		t.Fatalf("aborts=%d want 1 (attempt1 claim aborted before retry)", got)
	}
}
