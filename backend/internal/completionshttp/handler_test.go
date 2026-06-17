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
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
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
	return e.invokeCompletionsCtx(t, context.Background(), body)
}

// invokeCompletionsCtx 用指定父 ctx 跑请求，供模拟客户端断连(父 ctx 已取消)的脱钩测试。
func (e *completionsTestEnv) invokeCompletionsCtx(t *testing.T, parent context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewCompletionsHandler(e.deps))
	req := httptest.NewRequestWithContext(parent, http.MethodPost, "/v1/completions", bytes.NewBufferString(body))
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
	settles          []billing.SettleRequest
	aborts           []abortCall
	settleErr        error
	lastSettleCtx    context.Context
	lastSettleCtxErr error // ctx.Err() 在 Settle 被调那一刻的快照(defer cancel 之前)，守 WithoutCancel 脱钩
}

type abortCall struct {
	tenantID int64
	claimID  int64
	reason   string
}

func (s *recordingSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settles = append(s.settles, req)
	s.lastSettleCtx = ctx
	s.lastSettleCtxErr = ctx.Err() // 调用时刻快照：脱钩 ctx 在父被取消时此处应为 nil
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

// recordingRecoveryEnqueuer 是 settlementrecovery.Enqueuer 的 spy，记录交付后 settle 失败时
// 是否落 DLQ（recovery intent）。
type recordingRecoveryEnqueuer struct {
	calls      int
	lastEvt    dlq.Event
	retErr     error
	lastCtxErr error // ctx.Err() 在 Enqueue 被调那一刻的快照，守 enqueue 用 fresh(非过期 settle)ctx
}

func (q *recordingRecoveryEnqueuer) Enqueue(ctx context.Context, e dlq.Event) (int64, error) {
	q.calls++
	q.lastEvt = e
	q.lastCtxErr = ctx.Err()
	if q.retErr != nil {
		return 0, q.retErr
	}
	return 7, nil
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

func streamWithUsageBody() string {
	return strings.Join([]string{
		`data: {"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6},"choices":[]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
}

// TestCompletionsStreamSettleFailureEnqueuesRecoveryDLQ 守 S1-3：流式响应已交付客户端后
// settle 失败时，必须把 recovery intent 落 settlementrecovery DLQ，否则已交付 token 永久不计费
// (不可恢复钱丢失)——这正是 chat 路径有、completionshttp 此前缺的保护。
// Mutation: 删 settleStreamWithRecovery 里的 EnqueuePayload 调用 → spy.calls==0 → RED。
func TestCompletionsStreamSettleFailureEnqueuesRecoveryDLQ(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status:      http.StatusOK,
		body:        streamWithUsageBody(),
		contentType: "text/event-stream",
	})
	env.settler.settleErr = errors.New("settle tx aborted (post-delivery)")
	spy := &recordingRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = spy

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"stream please","stream":true}`)

	// 响应已交付(流式 200 + body 已 flush)。
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (stream already delivered)", rec.Code)
	}
	if len(env.settler.settles) != 1 {
		t.Fatalf("settle calls=%d want 1 (post-delivery settle)", len(env.settler.settles))
	}
	// 核心断言：settle 失败 → 必须落 DLQ recovery。
	if spy.calls != 1 {
		t.Fatalf("expected 1 settle-recovery DLQ enqueue on post-delivery settle failure, got %d", spy.calls)
	}
	// DLQ event 必须带可重结算的 claim/tenant + 正确 kind，否则 worker 无法重放。
	if spy.lastEvt.ClaimID == 0 || spy.lastEvt.TenantID == 0 {
		t.Fatalf("DLQ event missing claim/tenant: claim=%d tenant=%d", spy.lastEvt.ClaimID, spy.lastEvt.TenantID)
	}
	if spy.lastEvt.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("DLQ event kind=%q want post_delivery_settlement", spy.lastEvt.EventKind)
	}
}

// TestCompletionsStreamSettleUsesDetachedContext 守 S1-2 的核心原语 WithoutCancel：
// 交付后结算必须**脱钩**于请求 ctx——客户端在流末断连(父 ctx 取消)时，settle 仍须在一个
// 未被取消的 ctx 上跑，否则 Tx2 中止、已交付 token 永不计费。
// 判别构造：注入一个**已取消**的父 ctx(模拟断连)，断言 settle 被调那一刻其 ctx 未被取消
// (lastSettleCtxErr==nil) —— 只有 WithoutCancel 脱钩才能做到。
// Mutation: 把 attempt.go 的 WithoutCancel 去掉(仅留 WithTimeout(ex.ctx,...)) → settle ctx 继承
// 父的已取消状态 → lastSettleCtxErr==context.Canceled → RED。(仅删 WithTimeout 不影响本断言，
// 由姊妹断言"仍有 deadline"守住。)
func TestCompletionsStreamSettleUsesDetachedContext(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status:      http.StatusOK,
		body:        streamWithUsageBody(),
		contentType: "text/event-stream",
	})
	// 模拟客户端在流末断连：父 ctx 已取消。stub 依赖不检查 ctx，故请求仍能走到交付后结算。
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	rec := env.invokeCompletionsCtx(t, parent, `{"model":"legacy-public","prompt":"stream please","stream":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (stream delivered)", rec.Code)
	}
	if env.settler.lastSettleCtx == nil {
		t.Fatalf("settle was not invoked")
	}
	// 核心：尽管父 ctx 已取消，settle 调用时刻其 ctx 仍未被取消 → WithoutCancel 脱钩生效。
	if env.settler.lastSettleCtxErr != nil {
		t.Fatalf("settle ran on a ctx cancelled by client disconnect (err=%v) — WithoutCancel detachment missing (S1-2 regression)", env.settler.lastSettleCtxErr)
	}
	// 姊妹断言：脱钩后仍有 30s 超时上限(WithTimeout 半)，防 Tx2 永久挂住。
	if _, hasDeadline := env.settler.lastSettleCtx.Deadline(); !hasDeadline {
		t.Fatalf("post-delivery settle ctx has no deadline — WithTimeout missing")
	}
}

// TestCompletionsStreamSettleAndDLQEnqueueBothFail 守 #4：交付后 settle 失败、DLQ enqueue 也失败时，
// 兜底链的最后一环(P0 log)必须 (a) 不 panic、不改 HTTP(响应已发)，(b) enqueue 仍被尝试一次，
// (c) DLQ failure_reason 只记错误**类别**、不内插 raw settle 错误文本(防泄漏 prompt/凭证类内容)。
// Mutation: 把 billing.go 的 failureClass 从 privacy.ErrorClassFor 改成 err.Error() → 原始文本
// 进 FailureReason → 泄漏断言 RED。
func TestCompletionsStreamSettleAndDLQEnqueueBothFail(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status:      http.StatusOK,
		body:        streamWithUsageBody(),
		contentType: "text/event-stream",
	})
	// settle 错误文本里藏一个敏感子串，模拟 err 可能携带的不可外泄内容。
	const secret = "sk-leak-canary-9f3a"
	env.settler.settleErr = errors.New("settle tx aborted: " + secret)
	spy := &recordingRecoveryEnqueuer{retErr: errors.New("dlq insert failed")}
	env.deps.SettleRecoveryDLQ = spy

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"stream please","stream":true}`)

	// 响应已交付：两路兜底都失败也不能 panic / 改 HTTP 状态。
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (recovery failure must not change delivered HTTP)", rec.Code)
	}
	if len(env.settler.settles) != 1 {
		t.Fatalf("settle calls=%d want 1", len(env.settler.settles))
	}
	// enqueue 仍被尝试(即便失败)。
	if spy.calls != 1 {
		t.Fatalf("enqueue calls=%d want 1 (recovery attempted even when DLQ persist fails)", spy.calls)
	}
	// DLQ failure_reason 必须是错误类别，绝不内插 raw settle 错误文本。
	if strings.Contains(spy.lastEvt.FailureReason, secret) {
		t.Fatalf("DLQ failure_reason leaked raw settle error text: %q", spy.lastEvt.FailureReason)
	}
}

// TestCompletionsSettleRecoveryEnqueueUsesFreshContext 守 S2(#2)：当交付后 settle 因传入 ctx 的
// deadline 耗尽(DB 受压)而失败时，DLQ 兜底 enqueue 必须用一个**独立未过期**的 ctx，否则复用同一
// 已过期 ctx 会让 enqueue 的 INSERT 立即失败、recovery intent 落不了盘——DB 最受压时兜底最该工作。
// 直接单测 helper：传入一个已取消(模拟过期)的 ctx，断言 enqueue 收到的 ctx 未被取消。
// Mutation: billing.go 把 enqueue 的 enqCtx 改回复用传入 ctx → enqueue 收到已取消 ctx → RED。
func TestCompletionsSettleRecoveryEnqueueUsesFreshContext(t *testing.T) {
	settler := &recordingSettler{settleErr: errors.New("settle deadline exceeded")}
	spy := &recordingRecoveryEnqueuer{}
	ex := &execution{
		d:         Deps{Settler: settler, SettleRecoveryDLQ: spy},
		requestID: "req-fresh-ctx",
	}
	// 模拟 settle ctx 已过期/取消(deadline 耗尽场景)。
	expired, cancel := context.WithCancel(context.Background())
	cancel()

	err := ex.settleStreamWithRecovery(expired, billing.SettleRequest{ClaimID: 1, TenantID: 7})
	if err == nil {
		t.Fatalf("expected settle err to propagate")
	}
	if spy.calls != 1 {
		t.Fatalf("enqueue calls=%d want 1 (recovery must run even on a deadline-exceeded settle ctx)", spy.calls)
	}
	// 核心：enqueue 收到的 ctx 未被取消 → 用了 fresh(WithoutCancel)ctx，不受已过期 settle ctx 影响。
	if spy.lastCtxErr != nil {
		t.Fatalf("DLQ enqueue ran on the already-expired settle ctx (err=%v) — recovery intent would never persist (S2 #2)", spy.lastCtxErr)
	}
}
