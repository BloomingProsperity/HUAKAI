package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
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
	return &billing.ReserveResult{ClaimID: 999, AttemptSeq: 1}, nil
}

type insufficientBalanceClaimGate struct{}

func (insufficientBalanceClaimGate) Reserve(_ context.Context, _ billing.ReserveRequest) (*billing.ReserveResult, error) {
	return nil, billing.ErrInsufficientBalance
}

type stubSelector struct{}

func (stubSelector) Select(_ context.Context, _ pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{AccountID: 1}, nil
}

type stubSettler struct {
	abortCalls       int
	lastAbortClaimID int64
	lastAbortReason  string
}

func (s *stubSettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *stubSettler) Abort(_ context.Context, _ int64, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.abortCalls++
	s.lastAbortClaimID = claimID
	s.lastAbortReason = reason
	return nil
}

func (s *stubSettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
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
		// 真出站链路占位，让入口校验类测试通过 nil 守卫。
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

func TestHandler_AuthForbiddenReturns403(t *testing.T) {
	// 变异检查:若把 ErrForbidden 塌缩进通用鉴权错误路径,这里会返回 401,
	// 从而对客户端隐藏了某个已认证 key 的 IP 策略拒绝。
	d := minimalDeps()
	d.Auth = stubAuth{err: auth.ErrForbidden}

	rec := invokeHandler(t, d, validBody())

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("body=%s want forbidden code", rec.Body.String())
	}
}

func TestHandler_ModelAllowlistForbiddenBeforeRoute(t *testing.T) {
	// 变异检查:若 allowlist 未命中时放行(fail open),请求会继续进入
	// registry/routing,而不是返回这个稳定的 403。
	allowedModels := "gpt-4o"
	d := minimalDeps()
	d.Auth = stubAuth{identity: auth.Identity{
		TenantID:      7,
		APIKeyID:      11,
		UserID:        3,
		AllowedModels: &allowedModels,
	}}

	rec := invokeHandler(t, d, `{"model":"gpt-3.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"model_not_allowed"`) {
		t.Fatalf("body=%s want model_not_allowed code", rec.Body.String())
	}
}

func TestHandler_InsufficientBalanceReturnsClientParseable402(t *testing.T) {
	// 变异检查:若保留旧的 reserve_error 分支,会返回一个通用的 body,
	// 缺少 type=insufficient_quota、code=insufficient_balance,以及客户端为充值
	// UX 解析的那条精确中文消息。
	d := minimalDeps()
	d.ClaimGate = insufficientBalanceClaimGate{}

	rec := invokeHandler(t, d, validBody())

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d want 402 body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"type":"insufficient_quota"`,
		`"code":"insufficient_balance"`,
		`"message":"余额不足"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s missing %s", body, want)
		}
	}
	for _, bad := range []string{clienterr.CodeReserveError, "request reservation failed"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body=%s leaked generic reserve error marker %q", body, bad)
		}
	}
}

func TestHandler_ModerationBlockReturns403BeforeReserveAndAutoBanUsesIdentity(t *testing.T) {
	// 变异:移动/移除审核钩子会让被禁请求得以预留计费 claim 并到达 dispatch;
	// 调用计数断言会变红。
	enableHCSFDispatchForTest(t)
	claimGate := &moderationClaimGateSpy{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	audit := &chatModerationAuditSpy{}
	ban := &chatModerationBanSpy{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.CanonicalDispatcher = dispatcher
	d.ModerationScreener = moderation.NewScreener(moderation.ScreenerDeps{
		Config: chatModerationConfigStore{cfg: moderation.ModerationConfig{
			Enabled: true, FailClosed: true, SampleRatePct: 100,
			BanThreshold: 1, BanWindowSeconds: 3600,
		}},
		Keywords: &chatModerationKeywordStore{rules: []moderation.KeywordRule{{ID: 77, Keyword: "forbidden"}}},
		Audit:    audit,
		Ban:      ban,
	})

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"api_key_id":999999,"messages":[{"role":"user","content":"forbidden"}]}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), clienterr.CodeContentPolicyViolation) {
		t.Fatalf("body=%s want content policy code", rec.Body.String())
	}
	if claimGate.calls != 0 {
		t.Fatalf("reserve calls=%d want 0 for blocked moderation request", claimGate.calls)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 for blocked moderation request", dispatcher.calls)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events=%d want 1", len(audit.events))
	}
	if audit.events[0].PayloadHash == "" || audit.events[0].ReasonCode == "forbidden" {
		t.Fatalf("audit event leaked raw keyword or missed hash: %+v", audit.events[0])
	}
	if ban.calls != 1 {
		t.Fatalf("auto-ban calls=%d want 1", ban.calls)
	}
	if ban.events[0].APIKeyID != validIdentity().APIKeyID {
		t.Fatalf("auto-ban APIKeyID=%d want authenticated identity %d; body spoof must be ignored",
			ban.events[0].APIKeyID, validIdentity().APIKeyID)
	}
}

func TestHandler_ModerationCleanInputProceedsToReserveAndDispatch(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &moderationClaimGateSpy{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	ban := &chatModerationBanSpy{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.CanonicalDispatcher = dispatcher
	d.ModerationScreener = moderation.NewScreener(moderation.ScreenerDeps{
		Config:   chatModerationConfigStore{cfg: moderation.ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &chatModerationKeywordStore{rules: []moderation.KeywordRule{{ID: 77, Keyword: "forbidden"}}},
		Ban:      ban,
	})

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"clean request"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if claimGate.calls != 1 {
		t.Fatalf("reserve calls=%d want 1 for clean request", claimGate.calls)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1 for clean request", dispatcher.calls)
	}
	if ban.calls != 0 {
		t.Fatalf("auto-ban calls=%d want 0 for clean request", ban.calls)
	}
}

func TestHandler_ModerationBackendErrorFailsClosedBeforeReserve(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &moderationClaimGateSpy{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.ModerationScreener = moderation.NewScreener(moderation.ScreenerDeps{
		Config:   chatModerationConfigStore{cfg: moderation.ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &chatModerationKeywordStore{err: errors.New("keyword backend down")},
	})

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"clean request"}]}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want fail-closed 403 body=%s", rec.Code, rec.Body.String())
	}
	if claimGate.calls != 0 {
		t.Fatalf("reserve calls=%d want 0 on fail-closed moderation backend error", claimGate.calls)
	}
}

func TestHandler_ModerationBackendErrorFailOpenProceeds(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &moderationClaimGateSpy{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.CanonicalDispatcher = dispatcher
	d.ModerationScreener = moderation.NewScreener(moderation.ScreenerDeps{
		Config:   chatModerationConfigStore{cfg: moderation.ModerationConfig{Enabled: true, FailClosed: false}},
		Keywords: &chatModerationKeywordStore{err: errors.New("keyword backend down")},
	})

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"clean request"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 fail-open body=%s", rec.Code, rec.Body.String())
	}
	if claimGate.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("reserve/dispatch calls=%d/%d want 1/1 fail-open", claimGate.calls, dispatcher.calls)
	}
}

func TestHandler_ModerationDefaultOffWithoutScreenerPreservesChatPath(t *testing.T) {
	// 变异:在 chatHandlerConfigured 中把审核改成强制,或调用一个
	// nil/默认 screener,都会改变这条本已有效的 chat 路径。
	enableHCSFDispatchForTest(t)
	claimGate := &moderationClaimGateSpy{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"forbidden but moderation not configured"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want existing 200 path when moderation screener is nil; body=%s", rec.Code, rec.Body.String())
	}
	if claimGate.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("reserve/dispatch calls=%d/%d want 1/1 with moderation absent", claimGate.calls, dispatcher.calls)
	}
}

type moderationClaimGateSpy struct {
	calls   int
	claimID int64
	reqs    []billing.ReserveRequest
}

func (g *moderationClaimGateSpy) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.calls++
	g.reqs = append(g.reqs, req)
	claimID := g.claimID
	if claimID == 0 {
		claimID = 999
	}
	return &billing.ReserveResult{ClaimID: claimID}, nil
}

type chatModerationConfigStore struct {
	cfg moderation.ModerationConfig
	err error
}

func (s chatModerationConfigStore) GetConfig(context.Context, int64) (moderation.ModerationConfig, error) {
	if s.err != nil {
		return moderation.ModerationConfig{}, s.err
	}
	return s.cfg, nil
}

type chatModerationKeywordStore struct {
	rules []moderation.KeywordRule
	err   error
}

func (s *chatModerationKeywordStore) ListEnabled(context.Context, int64) ([]moderation.KeywordRule, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rules, nil
}

type chatModerationAuditSpy struct {
	events []moderation.ModerationEvent
}

func (s *chatModerationAuditSpy) Log(_ context.Context, event moderation.ModerationEvent, _ moderation.ModerationConfig) error {
	s.events = append(s.events, event)
	return nil
}

type chatModerationBanSpy struct {
	calls  int
	events []moderation.ModerationEvent
}

func (s *chatModerationBanSpy) RecordAndCheck(_ context.Context, event moderation.ModerationEvent, _ moderation.ModerationConfig) (moderation.BanResult, error) {
	s.calls++
	s.events = append(s.events, event)
	return moderation.BanResult{Count: int64(s.calls)}, nil
}

// 守:dedup until map 必须惰性回收已过期项,否则高账号 churn 下无限增长。window=0 让每个
// 条目立即过期;填满超阈值后再 admit 触发清理。变异: 去掉 purge → size 仍 > threshold,红。
func TestDedupingCredentialHotRefresher_PurgesExpiredEntries(t *testing.T) {
	r := newDedupingCredentialHotRefresher(nil, 0).(*dedupingCredentialHotRefresher)
	for i := int64(1); i <= int64(credentialHotRefreshPurgeThreshold)+5; i++ {
		r.admit(1, i)
	}
	r.admit(1, 999999) // 触发清理
	if got := len(r.until); got > credentialHotRefreshPurgeThreshold {
		t.Fatalf("until size=%d after purge, want <= %d (unbounded growth defect)", got, credentialHotRefreshPurgeThreshold)
	}
}
