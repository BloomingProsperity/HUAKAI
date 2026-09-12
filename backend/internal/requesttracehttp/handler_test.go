package requesttracehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	tracedb "github.com/BloomingProsperity/HUAKAI/internal/db/requesttracedb"
)

var fixedNow = time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func ptrI(v int64) *int64               { return &v }
func ptrS(v string) *string             { return &v }

// storeStub 用固定事实回答五条查询,并记录每条查询收到的范围参数(证明越权不触库、范围不放大)。
type storeStub struct {
	claim            *tracedb.GetRequestTraceClaimRow
	claimErr         error
	usage            []tracedb.ListRequestTraceUsageRecordsRow
	billing          []tracedb.ListRequestTraceBillingEventsRow
	audit            []tracedb.ListRequestTraceAuditEventsRow
	receipt          *tracedb.GetRequestTraceReceiptRow
	costReceipt      *tracedb.GetRequestTraceCostReceiptRow
	usageErr         error
	claimParams      []tracedb.GetRequestTraceClaimParams
	auditQueried     int
	auditIDs         []string
	receiptIDs       []string
	receiptAllowNull bool
}

func (s *storeStub) GetRequestTraceClaim(_ context.Context, p tracedb.GetRequestTraceClaimParams) (tracedb.GetRequestTraceClaimRow, error) {
	s.claimParams = append(s.claimParams, p)
	if s.claimErr != nil {
		return tracedb.GetRequestTraceClaimRow{}, s.claimErr
	}
	if s.claim == nil || (s.claim.LogicalRequestID != p.RequestID && !containsID(s.claim.HttpRequestIds, p.RequestID)) ||
		(p.TenantID > 0 && s.claim.TenantID != p.TenantID) || (p.UserID > 0 && s.claim.UserID != p.UserID) {
		return tracedb.GetRequestTraceClaimRow{}, pgx.ErrNoRows
	}
	return *s.claim, nil
}

func (s *storeStub) ListRequestTraceUsageRecords(_ context.Context, p tracedb.ListRequestTraceUsageRecordsParams) ([]tracedb.ListRequestTraceUsageRecordsRow, error) {
	if s.usageErr != nil {
		return nil, s.usageErr
	}
	if s.claim == nil || p.TenantID != s.claim.TenantID || p.ClaimID != s.claim.ID {
		return nil, nil
	}
	return s.usage, nil
}

func (s *storeStub) ListRequestTraceBillingEvents(_ context.Context, p tracedb.ListRequestTraceBillingEventsParams) ([]tracedb.ListRequestTraceBillingEventsRow, error) {
	if s.claim == nil || p.TenantID != s.claim.TenantID || p.ClaimID != s.claim.ID {
		return nil, nil
	}
	return s.billing, nil
}

func (s *storeStub) ListRequestTraceAuditEvents(_ context.Context, p tracedb.ListRequestTraceAuditEventsParams) ([]tracedb.ListRequestTraceAuditEventsRow, error) {
	s.auditQueried++
	s.auditIDs = p.RequestIds
	// 审计表按网关 HTTP 标识落账:只有把 claim 关联的 HTTP 标识带上才能查到。
	if s.claim == nil || p.TenantID != s.claim.TenantID || !containsID(p.RequestIds, "http-uuid-1") {
		return nil, nil
	}
	return s.audit, nil
}

func (s *storeStub) GetRequestTraceReceipt(_ context.Context, p tracedb.GetRequestTraceReceiptParams) (tracedb.GetRequestTraceReceiptRow, error) {
	s.receiptIDs = p.RequestIds
	s.receiptAllowNull = p.AllowNullTenant
	if s.receipt == nil || s.claim == nil || p.TenantID != s.claim.TenantID || !containsID(p.RequestIds, "http-uuid-1") {
		return tracedb.GetRequestTraceReceiptRow{}, pgx.ErrNoRows
	}
	return *s.receipt, nil
}

func (s *storeStub) GetRequestTraceCostReceipt(_ context.Context, p tracedb.GetRequestTraceCostReceiptParams) (tracedb.GetRequestTraceCostReceiptRow, error) {
	if s.costReceipt == nil || s.claim == nil || p.TenantID != s.claim.TenantID || !containsID(p.RequestIds, "http-uuid-1") {
		return tracedb.GetRequestTraceCostReceiptRow{}, pgx.ErrNoRows
	}
	return *s.costReceipt, nil
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.ident, a.err
}

func sampleStore() *storeStub {
	routing := `{"selection_layer":"fresh","affinity_key_class":"none","capability_outcome":"native",` +
		`"candidate_counts_by_exclusion":{"auth_cooldown":1,"health":2},"scoring_policy_version":"v3",` +
		`"retry_attempt_number":1,"per_request_exclusion_summary":[{"account_id":501,"reason":"auth_cooldown"}],` +
		`"forced_route_override_actor":null,"wait_action":null}`
	return &storeStub{
		claim: &tracedb.GetRequestTraceClaimRow{
			ID: 77, TenantID: 7, LogicalRequestID: "req_abc-123", EndpointFamily: "chat", RequestedModel: "gpt-x",
			PoolingGroupID: ptrI(3), PoolName: ptrS("primary"), APIKeyID: 44, UserID: 9, RequestClass: "standard",
			BillingEffect: "user_charge", ProviderAccountID: ptrI(502), AttemptSeq: 2,
			PredictedCost: decimal.RequireFromString("0.01"), ActualCost: decimal.NewNullDecimal(decimal.RequireFromString("0.0042")),
			CurrencyCode: "USD", Status: "committed", AbortedReason: ptrS("upstream_error_5xx"),
			ReservedAt: ts(fixedNow.Add(-time.Minute)), SettledAt: ts(fixedNow),
			HttpRequestIds: []string{"http-uuid-1"},
		},
		costReceipt: &tracedb.GetRequestTraceCostReceiptRow{
			RequestID: "http-uuid-1", ReceiptSequence: 1, Model: "gpt-x", InputTokens: 200, OutputTokens: 305, CachedTokens: 20, CostUsdMicros: 4200, CreatedAt: ts(fixedNow),
		},
		usage: []tracedb.ListRequestTraceUsageRecordsRow{
			{
				// 拿到账号后中止的尝试:结算链写入的形状是 unknown_termination + inferred + 账务态失败 + 零费用,
				// 中止分类码记在 stream_terminated_reason。
				ID: 1, AttemptSeq: 1, ProviderAccountID: ptrI(501), ProviderAccountName: ptrS("acct-a"), ChannelID: ptrI(31), ProviderCode: ptrS("openai"),
				RequestedModel: "gpt-x", UpstreamModel: ptrS("gpt-x-2026"), Stream: true, EndClass: "unknown_termination", UsageSource: "inferred",
				SettlementSource: "provider_upstream", PendingReconciliation: true, StreamState: 3, StreamTerminatedReason: ptrS("upstream_error_5xx"),
				TokensInput: 100, TokensOutput: 0, ActualCost: decimal.Zero, InputCost: decimal.Zero,
				OutputCost: decimal.Zero, RoutingReason: []byte(routing), ProtocolLoss: []byte(`[{"kind":"thinking"}]`),
				RequestedAt: ts(fixedNow.Add(-50 * time.Second)), UpstreamRequestAt: ts(fixedNow.Add(-49 * time.Second)),
				FirstByteAt: ts(fixedNow.Add(-48 * time.Second)), SettledAt: ts(fixedNow.Add(-40 * time.Second)),
			},
			{
				ID: 2, AttemptSeq: 2, ProviderAccountID: ptrI(502), ProviderAccountName: ptrS("acct-b"), ChannelID: ptrI(31), ProviderCode: ptrS("openai"),
				RequestedModel: "gpt-x", UpstreamModel: ptrS("gpt-x-2026"), Stream: true, EndClass: "stream_end_graceful", UsageSource: "reported",
				SettlementSource: "provider_upstream", StreamState: 2, DeliveredTokenCount: 300,
				TokensInput: 100, TokensOutput: 300, CacheReadTokens: 20, ActualCost: decimal.RequireFromString("0.004"), InputCost: decimal.RequireFromString("0.001"),
				OutputCost: decimal.RequireFromString("0.003"), RoutingReason: []byte(routing), ProtocolLoss: []byte(`[]`),
				RequestedAt: ts(fixedNow.Add(-39 * time.Second)), SettledAt: ts(fixedNow),
			},
		},
		billing: []tracedb.ListRequestTraceBillingEventsRow{
			{ID: 1, EventType: "claim_aborted", ActualCostSigned: decimal.Zero, EndClass: ptrS("upstream_error_5xx"), OccurredAt: ts(fixedNow.Add(-40 * time.Second))},
			{ID: 2, EventType: "claim_committed", ActualCostSigned: decimal.RequireFromString("0.0042"), EndClass: ptrS("stream_end_graceful"), OccurredAt: ts(fixedNow)},
		},
		audit: []tracedb.ListRequestTraceAuditEventsRow{
			{Source: "channel_health", EventType: "channel_health_degraded", Reason: "upstream_5xx", PreviousState: "active", NewState: "degraded", ProviderAccountID: ptrI(501), OccurredAt: ts(fixedNow.Add(-40 * time.Second))},
			{Source: "rate_limit", EventType: "rate_limited_set", Reason: "upstream_429", ProviderAccountID: ptrI(501), OccurredAt: ts(fixedNow.Add(-45 * time.Second))},
			{Source: "pool_routing", EventType: "pool_exhausted", Reason: "all candidates excluded by operator policy X", OccurredAt: ts(fixedNow.Add(-44 * time.Second))},
		},
		receipt: &tracedb.GetRequestTraceReceiptRow{
			LedgerID: "ledger-1", OccurredAt: ts(fixedNow), PubkeyFingerprint: "0123456789abcdef",
			ModelChain: []byte(`["gpt-x","gpt-x-2026"]`), HopChain: []byte(`[{"hop":"upstream-a"}]`),
		},
	}
}

func adminRequest(t *testing.T, d Deps, target string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	router := chi.NewRouter()
	h := NewAdminHandler(d)
	router.Get("/admin/v1/requests/{request_id}/trace", h)
	router.Get("/admin/v1/requests/{request_id_host}/{request_id_tail}/trace", h)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var body Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	}
	return rec, body
}

func userRequest(t *testing.T, d Deps, target string, ident *auth.SessionIdentity) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/v1/me/requests/{request_id}/trace", NewUserHandler(d))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if ident != nil {
		req = req.WithContext(auth.ContextWithSession(req.Context(), *ident))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var body Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	}
	return rec, body
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("状态=%d body=%s,期望 %d %s", rec.Code, rec.Body.String(), status, code)
	}
}

// 部署者全量档:头部含租户/用户/Key 数字标识与最后一跳账号;尝试含账号代号、渠道、选号明细(含逐账号排除);
// 事件含账本与账号级审计并按时间排序;收据含 hop_chain;coverage 为 full/within。
func TestPlatformAdminSeesFullRedactedProjection(t *testing.T) {
	store := sampleStore()
	rec, body := adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: store, Now: func() time.Time { return fixedNow }}, "/admin/v1/requests/req_abc-123/trace")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	if body.Viewer != ViewerPlatform || body.Request.TenantID == nil || *body.Request.TenantID != 7 || body.Request.UserID == nil || body.Request.APIKeyID == nil {
		t.Fatalf("部署者应看到租户/用户/Key 数字标识: %+v", body.Request)
	}
	if body.Request.FinalAccount == nil || body.Request.FinalAccount.ProviderAccountID != 502 || body.Request.AbortedReason == nil {
		t.Fatalf("部署者应看到最后一跳账号与中止原因: %+v", body.Request)
	}
	if len(body.Attempts) != 2 || body.Attempts[0].Outcome != "failed" || body.Attempts[1].Outcome != "success" {
		t.Fatalf("尝试应按 attempt_seq 且结果分档正确: %+v", body.Attempts)
	}
	if body.Attempts[0].FailureCategory != "upstream" || body.Attempts[0].TerminatedReason == nil || *body.Attempts[0].TerminatedReason != "upstream_error_5xx" || body.Attempts[1].FailureCategory != "" {
		t.Fatalf("失败尝试应带粗分类与运营侧中止分类码: %+v", body.Attempts[0])
	}
	a := body.Attempts[0]
	if a.Account == nil || a.Account.ProviderAccountID != 501 || a.Account.Name == nil || *a.Account.Name != "acct-a" || a.Account.ChannelID == nil {
		t.Fatalf("部署者应看到账号代号: %+v", a.Account)
	}
	if a.Routing == nil || a.Routing.SelectionLayer != "fresh" || a.Routing.ExclusionCounts["health"] != 2 || len(a.Routing.ExcludedAccounts) != 1 || a.Routing.ExcludedAccounts[0].ProviderAccountID != 501 {
		t.Fatalf("部署者应看到含逐账号排除的选号摘要: %+v", a.Routing)
	}
	if a.ProtocolLossCount != 1 || a.Usage.DeliveredTokens != 0 || a.Usage.ActualCost != "0" || !a.PendingReconciliation {
		t.Fatalf("尝试用量/协议损失字段不符: %+v", a)
	}
	if len(body.Events) != 5 || body.Events[0].Source != "rate_limit" || body.Events[4].EventType != "claim_committed" {
		t.Fatalf("事件应按时间排序且含账本与审计: %+v", body.Events)
	}
	for _, ev := range body.Events {
		if ev.Source == "pool_routing" && ev.Reason == "" {
			t.Fatalf("部署者应看到路由审计全文: %+v", ev)
		}
	}
	var health *Event
	for i := range body.Events {
		if body.Events[i].Source == "channel_health" {
			health = &body.Events[i]
		}
	}
	if health == nil || health.Account == nil || health.Account.ProviderAccountID != 501 || health.NewState != "degraded" {
		t.Fatalf("审计事件应带账号代号与状态转换: %+v", health)
	}
	if body.Receipt == nil || body.Receipt.LedgerID != "ledger-1" || len(body.Receipt.HopChain) == 0 || len(body.Receipt.ModelChain) == 0 {
		t.Fatalf("部署者应看到含 hop_chain 的收据: %+v", body.Receipt)
	}
	if body.Coverage.AttemptsDetail != AttemptsDetailFull || body.Coverage.DetailRetention != DetailRetentionWithin || body.Coverage.UnknownAttemptAccount != 0 ||
		body.Coverage.AbortedAttempts != 1 || body.Coverage.CommittedAttempts != 1 || !body.Coverage.HasReceipt {
		t.Fatalf("coverage 不符: %+v", body.Coverage)
	}
	if body.Totals.TokensInput != 200 || body.Totals.TokensOutput != 300 || body.Totals.ActualCost != "0.004" {
		t.Fatalf("合计不符: %+v", body.Totals)
	}
	// 两把请求标识都回传;审计/收据按 claim 关联的 HTTP 标识 join(路径给的是逻辑标识)。
	if body.Request.RequestID != "req_abc-123" || body.Request.LogicalRequestID != "req_abc-123" || len(body.Request.HTTPRequestIDs) != 1 || body.Request.HTTPRequestIDs[0] != "http-uuid-1" {
		t.Fatalf("请求标识回传不符: %+v", body.Request)
	}
	if !containsID(store.auditIDs, "http-uuid-1") || !containsID(store.auditIDs, "req_abc-123") || !containsID(store.receiptIDs, "http-uuid-1") {
		t.Fatalf("审计与收据查询必须带上全部关联标识: audit=%v receipt=%v", store.auditIDs, store.receiptIDs)
	}
	if body.CostReceipt == nil || body.CostReceipt.CostUSDMicros != 4200 || !body.Coverage.HasCostReceipt {
		t.Fatalf("应带用户面费用收据摘要: %+v", body.CostReceipt)
	}
	if !store.receiptAllowNull {
		t.Fatal("部署者档位应允许匹配租户未知的历史信任链条目")
	}
	// 用响应头里的 HTTP 请求标识查询也应找到同一 claim。
	rec2, body2 := adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: sampleStore()}, "/admin/v1/requests/http-uuid-1/trace")
	if rec2.Code != http.StatusOK || body2.Request.LogicalRequestID != "req_abc-123" || body2.Request.RequestID != "http-uuid-1" || body2.Receipt == nil {
		t.Fatalf("按 HTTP 请求标识必须找到同一 claim 与收据: code=%d %+v", rec2.Code, body2.Request)
	}
	// 部署者省略 tenant_id → 跨租户查询(TenantID=0),不按用户收敛。
	if len(store.claimParams) != 1 || store.claimParams[0].TenantID != 0 || store.claimParams[0].UserID != 0 {
		t.Fatalf("部署者省略 tenant_id 应跨租户查询: %+v", store.claimParams)
	}
}

// 租户管理员档:看到本租户资源代号与选号摘要,但看不到逐账号排除、租户/用户/Key 数字标识、hop_chain;
// 省略 tenant_id 锁本租户;显式他人租户 → 403 且不触库。
func TestTenantOperatorRedaction(t *testing.T) {
	store := sampleStore()
	deps := Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7, UserID: 2}}, Store: store}
	rec, body := adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	if body.Viewer != ViewerTenant || body.Request.TenantID != nil || body.Request.UserID != nil || body.Request.APIKeyID != nil {
		t.Fatalf("租户管理员不得看到租户/用户/Key 数字标识: %+v", body.Request)
	}
	if body.Request.FinalAccount == nil || body.Attempts[0].Account == nil || body.Attempts[0].Account.Name == nil {
		t.Fatalf("租户管理员应看到账号代号: %+v", body.Attempts[0].Account)
	}
	if body.Attempts[0].Routing == nil || len(body.Attempts[0].Routing.ExcludedAccounts) != 0 || body.Attempts[0].Routing.ExclusionCounts["auth_cooldown"] != 1 {
		t.Fatalf("租户管理员只看排除计数,不看逐账号排除: %+v", body.Attempts[0].Routing)
	}
	if body.Receipt == nil || len(body.Receipt.HopChain) != 0 || len(body.Receipt.ModelChain) == 0 {
		t.Fatalf("租户管理员不得看到 hop_chain: %+v", body.Receipt)
	}
	if store.receiptAllowNull {
		t.Fatal("租户管理员不得匹配租户未知的信任链条目")
	}
	if len(body.Events) != 5 {
		t.Fatalf("租户管理员应看到账号级审计事件: %+v", body.Events)
	}
	if store.claimParams[0].TenantID != 7 {
		t.Fatalf("省略 tenant_id 应锁本租户: %+v", store.claimParams)
	}
	for _, leaked := range []string{`"tenant_id"`, `"api_key_id"`, `"hop_chain"`, `"excluded_accounts"`} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("租户管理员响应泄漏 %s: %s", leaked, rec.Body.String())
		}
	}

	before := len(store.claimParams)
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace?tenant_id=9")
	expectError(t, rec, http.StatusForbidden, "admin_forbidden")
	if len(store.claimParams) != before {
		t.Fatal("显式他人租户必须在触库前拒绝")
	}
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace?tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("显式本租户应放行: %d", rec.Code)
	}
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace?tenant_id=all")
	expectError(t, rec, http.StatusBadRequest, "invalid_tenant_id")

	// 他租户的标识(不带 tenant_id)、不存在、账本也没有的标识:租户管理员得到字节相同的 404。
	foreignStore := sampleStore()
	foreignStore.claim.TenantID = 9
	foreign, _ := adminRequest(t, Deps{Auth: deps.Auth, Store: foreignStore}, "/admin/v1/requests/req_abc-123/trace")
	missing, _ := adminRequest(t, deps, "/admin/v1/requests/req_nope/trace")
	expectError(t, foreign, http.StatusNotFound, "request_not_found")
	if foreign.Body.String() != missing.Body.String() || foreign.Code != missing.Code {
		t.Fatalf("租户管理员对他租户与不存在必须同一应答: %s vs %s", foreign.Body.String(), missing.Body.String())
	}
	// 路由审计的自由文本 reason 对租户管理员隐藏,事件类型保留。
	_, body = adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace")
	for _, ev := range body.Events {
		if ev.Source == "pool_routing" && ev.Reason != "" {
			t.Fatalf("租户管理员不得看到路由审计自由文本: %+v", ev)
		}
	}
}

// 最终用户档:只看模型、池名、状态、用量、费用、账本事件与收据摘要;不含账号代号、渠道、选号明细、
// 账号级审计、hop_chain、中止原因、池 id;非本人请求与不存在同一 404 形状;审计事件查询根本不发起。
func TestUserRedactionAndIndistinguishableNotFound(t *testing.T) {
	store := sampleStore()
	deps := Deps{Store: store}
	own := &auth.SessionIdentity{TenantID: 7, UserID: 9}
	rec, body := userRequest(t, deps, "/v1/me/requests/req_abc-123/trace", own)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	if body.Viewer != ViewerUser || body.Request.PoolName == nil || *body.Request.PoolName != "primary" || body.Request.Status != "committed" {
		t.Fatalf("用户应看到模型/池名/状态: %+v", body.Request)
	}
	if body.Request.FinalAccount != nil || body.Request.PoolGroupID != nil || body.Request.AbortedReason != nil || body.Request.TenantID != nil {
		t.Fatalf("用户不得看到账号/池 id/中止原因/租户: %+v", body.Request)
	}
	if len(body.Attempts) != 2 || body.Attempts[0].Account != nil || body.Attempts[0].Routing != nil || body.Attempts[1].Usage.TokensOutput != 300 || body.Attempts[1].Usage.ActualCost != "0.004" {
		t.Fatalf("用户尝试只含结果与用量: %+v", body.Attempts)
	}
	if body.Attempts[0].FailureCategory != "upstream" || body.Attempts[0].TerminatedReason != nil {
		t.Fatalf("用户应看到失败粗分类但不看原始分类码: %+v", body.Attempts[0])
	}
	if len(body.Events) != 2 || body.Events[0].Source != "billing" || body.Events[1].Source != "billing" {
		t.Fatalf("用户只看账本事件: %+v", body.Events)
	}
	if store.auditQueried != 0 {
		t.Fatal("用户档位不得发起账号级审计查询")
	}
	if body.Receipt == nil || len(body.Receipt.HopChain) != 0 || len(body.Receipt.ModelChain) != 0 || body.Receipt.PubkeyFingerprint == "" {
		t.Fatalf("用户收据不含 hop_chain / model_chain: %+v", body.Receipt)
	}
	for _, leaked := range []string{`"account"`, `"channel_id"`, `"provider_account_id"`, `"routing"`, `"hop_chain"`, `"model_chain"`, `"acct-a"`, `"aborted_reason"`, `"terminated_reason"`, `"api_key_id"`, `"provider"`, `"upstream_model"`, `"openai"`, `gpt-x-2026`} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("用户响应泄漏 %s: %s", leaked, rec.Body.String())
		}
	}
	if body.CostReceipt == nil || body.CostReceipt.CostUSDMicros != 4200 {
		t.Fatalf("用户应看到自己的费用收据摘要: %+v", body.CostReceipt)
	}
	// 用户:用量明细已不在但账本仍在 → 200 + expired,而不是 404。
	expired := sampleStore()
	expired.usage = nil
	rec, body = userRequest(t, Deps{Store: expired, Now: func() time.Time { return fixedNow.Add(time.Hour) }}, "/v1/me/requests/req_abc-123/trace", own)
	if rec.Code != http.StatusOK || body.Coverage.DetailRetention != DetailRetentionExpired || body.Receipt == nil {
		t.Fatalf("用户过期但有账本应返回摘要: code=%d cov=%+v", rec.Code, body.Coverage)
	}
	if store.claimParams[0].TenantID != 7 || store.claimParams[0].UserID != 9 {
		t.Fatalf("用户查询必须按会话租户与用户收敛: %+v", store.claimParams[0])
	}

	// 非本人(同租户其他用户)、他租户、不存在:三者响应完全相同。
	other, _ := userRequest(t, deps, "/v1/me/requests/req_abc-123/trace", &auth.SessionIdentity{TenantID: 7, UserID: 10})
	foreign, _ := userRequest(t, deps, "/v1/me/requests/req_abc-123/trace", &auth.SessionIdentity{TenantID: 8, UserID: 9})
	missing, _ := userRequest(t, deps, "/v1/me/requests/req_nope/trace", own)
	for name, r := range map[string]*httptest.ResponseRecorder{"other_user": other, "foreign_tenant": foreign, "missing": missing} {
		expectError(t, r, http.StatusNotFound, "request_not_found")
		if r.Body.String() != missing.Body.String() {
			t.Fatalf("%s 的应答必须与不存在完全相同: %s vs %s", name, r.Body.String(), missing.Body.String())
		}
	}
	rec, _ = userRequest(t, deps, "/v1/me/requests/req_abc-123/trace", nil)
	expectError(t, rec, http.StatusUnauthorized, "session_required")
	// 租户管理员的 404 与用户的 404 也必须字节相同(不同入口不能成为探测差异)。
	tenantMissing, _ := adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, Store: store}, "/admin/v1/requests/req_nope/trace")
	if tenantMissing.Body.String() != missing.Body.String() {
		t.Fatalf("租户管理员与用户的 404 必须同一形状: %s vs %s", tenantMissing.Body.String(), missing.Body.String())
	}
	// 用户端点忽略任何范围参数:tenant_id / user_id 查询参数不能放大范围。
	widened, _ := userRequest(t, deps, "/v1/me/requests/req_abc-123/trace?tenant_id=7&user_id=9", &auth.SessionIdentity{TenantID: 8, UserID: 9})
	expectError(t, widened, http.StatusNotFound, "request_not_found")
}

// 非法标识 400(不触库);Store 缺失 503;查询失败 503(不返回空投影);部署者查不存在 → 404。
func TestFailClosedPaths(t *testing.T) {
	store := sampleStore()
	deps := Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: store}
	rec, _ := adminRequest(t, deps, "/admin/v1/requests/bad%20id/trace")
	expectError(t, rec, http.StatusBadRequest, "invalid_request_id")
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/"+strings.Repeat("a", 257)+"/trace")
	expectError(t, rec, http.StatusBadRequest, "invalid_request_id")
	if len(store.claimParams) != 0 {
		t.Fatal("非法标识不得触库")
	}
	// 客户端幂等键可含一个斜杠、模型回退派生标识含 '#'(百分号编码后)都是合法标识,按精确值查库。
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/tenant-a/idem-77/trace")
	expectError(t, rec, http.StatusNotFound, "request_not_found")
	if store.claimParams[len(store.claimParams)-1].RequestID != "tenant-a/idem-77" {
		t.Fatalf("含斜杠的标识应原样查库: %+v", store.claimParams)
	}
	// 百分号编码的斜杠由路由层原样保留(不解码为路径分隔符),按字面值精确查库。
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/a%2Fb%2Fc/trace")
	expectError(t, rec, http.StatusNotFound, "request_not_found")
	if store.claimParams[len(store.claimParams)-1].RequestID != "a%2Fb%2Fc" {
		t.Fatalf("编码斜杠应原样查库: %+v", store.claimParams[len(store.claimParams)-1])
	}
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_abc-123%23mf:1a2b/trace")
	expectError(t, rec, http.StatusNotFound, "request_not_found")
	if store.claimParams[len(store.claimParams)-1].RequestID != "req_abc-123#mf:1a2b" {
		t.Fatalf("模型回退派生标识应解码后原样查库: %+v", store.claimParams)
	}
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_missing/trace")
	expectError(t, rec, http.StatusNotFound, "request_not_found")
	store.usageErr = errors.New("db down")
	rec, _ = adminRequest(t, deps, "/admin/v1/requests/req_abc-123/trace")
	expectError(t, rec, http.StatusServiceUnavailable, "request_trace_query_failed")
	rec, _ = adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}}, "/admin/v1/requests/req_abc-123/trace")
	expectError(t, rec, http.StatusServiceUnavailable, "gateway_not_configured")
	rec, _ = adminRequest(t, Deps{Auth: authStub{err: admin.ErrAdminForbidden}, Store: store}, "/admin/v1/requests/req_abc-123/trace")
	expectError(t, rec, http.StatusForbidden, "admin_forbidden")
	rec, _ = adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store}, "/admin/v1/requests/req_abc-123/trace?tenant_id=8")
	expectError(t, rec, http.StatusNotFound, "request_not_found")
}

// 降级标记:零用量中止只留账本事件 → partial + unknown_account_attempts;用量全部过期但账本已提交 →
// final_only + expired,头部与收据仍返回;既无用量也无账本事件 → none + no_detail;in-flight 加注。
func TestCoverageDegradationMarkers(t *testing.T) {
	deps := func(s *storeStub) Deps {
		// 结算发生在 fixedNow;观察时刻取一小时后,超出用量异步落盘的宽限窗。
		return Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: s, Now: func() time.Time { return fixedNow.Add(time.Hour) }}
	}
	partial := sampleStore()
	partial.usage = partial.usage[1:] // 第一次中止是零用量:没有用量行
	_, body := adminRequest(t, deps(partial), "/admin/v1/requests/req_abc-123/trace")
	if body.Coverage.AttemptsDetail != AttemptsDetailPartial || body.Coverage.UnknownAttemptAccount != 1 || body.Coverage.DetailRetention != DetailRetentionWithin {
		t.Fatalf("零用量中止应标 partial 且账号未知计数 1: %+v", body.Coverage)
	}

	expired := sampleStore()
	expired.usage = nil
	rec, body := adminRequest(t, deps(expired), "/admin/v1/requests/req_abc-123/trace")
	if rec.Code != http.StatusOK || body.Coverage.AttemptsDetail != AttemptsDetailFinalOnly || body.Coverage.DetailRetention != DetailRetentionExpired {
		t.Fatalf("用量过期应仍返回头部并标 final_only/expired: code=%d cov=%+v", rec.Code, body.Coverage)
	}
	if body.Request.RequestID != "req_abc-123" || body.Receipt == nil || body.Request.FinalAccount == nil || len(body.Attempts) != 0 {
		t.Fatalf("过期后头部、最后一跳与收据仍应返回: %+v", body)
	}
	// 刚结算(宽限窗内)且无用量行 → pending 而不是 expired。
	fresh := sampleStore()
	fresh.usage = nil
	_, body = adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: fresh, Now: func() time.Time { return fixedNow.Add(time.Minute) }}, "/admin/v1/requests/req_abc-123/trace")
	if body.Coverage.DetailRetention != DetailRetentionPending || !containsNote(body.Coverage.Notes, "usage_detail_not_yet_persisted") {
		t.Fatalf("宽限窗内无用量应标 pending: %+v", body.Coverage)
	}
	// 对账追加事件不计入尝试数;同一标识命中多条 claim 时打注记;选号解释损坏时打注记。
	recon := sampleStore()
	recon.billing = append(recon.billing, tracedb.ListRequestTraceBillingEventsRow{ID: 9, EventType: "reconciliation_appended", ActualCostSigned: decimal.RequireFromString("0.0001"), OccurredAt: ts(fixedNow.Add(time.Minute))})
	recon.claim.CandidateCount = 2
	recon.usage[1].RoutingReason = []byte(`{not json`)
	_, body = adminRequest(t, deps(recon), "/admin/v1/requests/req_abc-123/trace")
	if body.Coverage.AbortedAttempts != 1 || body.Coverage.CommittedAttempts != 1 || body.Coverage.AttemptsDetail != AttemptsDetailFull || body.Coverage.UnknownAttemptAccount != 0 {
		t.Fatalf("对账追加事件不得计入尝试数: %+v", body.Coverage)
	}
	if !containsNote(body.Coverage.Notes, "multiple_claims_for_id") || !containsNote(body.Coverage.Notes, "routing_digest_unavailable") {
		t.Fatalf("应带多 claim 与选号解释不可用注记: %+v", body.Coverage.Notes)
	}
	if len(body.Events) != 6 {
		t.Fatalf("对账追加事件仍应出现在时间线: %d", len(body.Events))
	}

	aborted := sampleStore()
	aborted.claim.Status = "aborted"
	aborted.claim.AbortedReason = ptrS("quota_denied")
	_, body = adminRequest(t, deps(aborted), "/admin/v1/requests/req_abc-123/trace")
	if body.Request.FailureCategory != "quota" {
		t.Fatalf("已中止请求应带粗分类: %+v", body.Request)
	}
	_, userBody := userRequest(t, Deps{Store: aborted}, "/v1/me/requests/req_abc-123/trace", &auth.SessionIdentity{TenantID: 7, UserID: 9})
	if userBody.Request.FailureCategory != "quota" || userBody.Request.AbortedReason != nil {
		t.Fatalf("用户应看到粗分类而非原始分类码: %+v", userBody.Request)
	}

	empty := sampleStore()
	empty.usage, empty.billing, empty.audit, empty.receipt = nil, nil, nil, nil
	empty.claim.Status = "reserving"
	_, body = adminRequest(t, deps(empty), "/admin/v1/requests/req_abc-123/trace")
	if body.Coverage.AttemptsDetail != AttemptsDetailNone || body.Coverage.DetailRetention != DetailRetentionNoDetail || body.Receipt != nil {
		t.Fatalf("无任何事实段应标 none/no_detail: %+v", body.Coverage)
	}
	if !containsNote(body.Coverage.Notes, "in_flight_or_unsettled") || !containsNote(body.Coverage.Notes, "no_receipt") {
		t.Fatalf("应带 in-flight 与无收据注记: %+v", body.Coverage.Notes)
	}
}

func containsNote(notes []string, want string) bool {
	for _, n := range notes {
		if n == want {
			return true
		}
	}
	return false
}

// 结果档:先看账务态与交付量,再看结束分类——中止写入的行(unknown_termination、账务态失败、零交付)
// 必须是 failed 而不是 partial;确有交付但无收尾标记才是 partial;取消与成功按结束分类。
// 判别:把 outcomeForAttempt 改回只看结束分类 → 第一条用例(中止行)红。
func TestOutcomeForAttempt(t *testing.T) {
	row := func(end string, state int16, delivered int64, out int32) tracedb.ListRequestTraceUsageRecordsRow {
		return tracedb.ListRequestTraceUsageRecordsRow{EndClass: end, StreamState: state, DeliveredTokenCount: delivered, TokensOutput: out}
	}
	cases := []struct {
		name string
		row  tracedb.ListRequestTraceUsageRecordsRow
		want string
	}{
		{"中止写入的行", row("unknown_termination", streamStateFailed, 0, 0), "failed"},
		{"有交付无收尾标记", row("stream_end_no_terminal_marker", int16(2), 40, 40), "partial"},
		{"用量歧义但有交付", row("usage_ambiguous", int16(2), 3, 3), "partial"},
		{"5xx 零交付", row("upstream_error_5xx", int16(2), 0, 0), "failed"},
		{"5xx 有交付仍失败", row("upstream_error_5xx", int16(2), 12, 12), "failed"},
		{"正常收尾", row("stream_end_graceful", int16(2), 100, 100), "success"},
		{"非流式", row("non_streaming", int16(2), 0, 100), "success"},
		{"客户端断开", row("client_disconnect", int16(2), 5, 5), "cancelled"},
		{"编排取消", row("orchestrator_cancelled", streamStateFailed, 0, 0), "cancelled"},
		{"首字超时", row("first_token_timeout", streamStateFailed, 0, 0), "failed"},
	}
	for _, tc := range cases {
		if got := outcomeForAttempt(tc.row); got != tc.want {
			t.Fatalf("%s → %s,期望 %s", tc.name, got, tc.want)
		}
	}
}

// 失败粗分类覆盖合同要求的九类,且原始网关分类码不会原样透出给最终用户。
func TestFailureCategory(t *testing.T) {
	cases := map[string]string{
		"upstream_auth_failure": "auth", "credential_resolve_error": "auth", "invalid_grant": "auth",
		"quota_denied": "quota", "insufficient_balance": "quota",
		"upstream_rate_limit": "rate_limit", "overloaded": "rate_limit",
		"moderation_blocked": "security_policy", "content_policy_violation": "security_policy",
		"invalid_request_body": "invalid_request", "streaming_translation_not_supported": "invalid_request", "upstream_response_too_large": "invalid_request",
		"pool_select_no_account": "routing_unavailable", "queue_wait_cancelled": "routing_unavailable", "pool_exhausted": "routing_unavailable",
		"upstream_error_5xx": "upstream", "first_token_timeout": "upstream", "upstream_dispatch_error": "upstream",
		"pool_no_capacity": "routing_unavailable", "transport_connection_refused": "upstream", "transport_tls_handshake_failed": "upstream",
		"agent_task_invalid": "invalid_request", "credential_protocol_incompatible": "invalid_request", "upstream_forbidden": "upstream",
		"audit_ledger_error": "internal", "pricing_unavailable": "internal", "cache_key_error": "internal",
		"client_disconnect": "other", "unknown_termination": "other", "": "other",
	}
	for code, want := range cases {
		if got := failureCategory(code); got != want {
			t.Fatalf("%q → %s,期望 %s", code, got, want)
		}
	}
}

// 事件按真实时刻排序,而不是按格式化字符串:小数位长度不同的时刻不得颠倒。
func TestEventsSortedByInstantNotString(t *testing.T) {
	store := sampleStore()
	base := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	store.billing = []tracedb.ListRequestTraceBillingEventsRow{
		{ID: 1, EventType: "claim_aborted", ActualCostSigned: decimal.Zero, OccurredAt: ts(base.Add(500 * time.Millisecond))},   // ...:00.5Z
		{ID: 2, EventType: "claim_committed", ActualCostSigned: decimal.Zero, OccurredAt: ts(base.Add(123 * time.Millisecond))}, // ...:00.123Z
	}
	store.audit = nil
	_, body := adminRequest(t, Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store}, "/admin/v1/requests/req_abc-123/trace")
	if len(body.Events) != 2 || body.Events[0].EventType != "claim_committed" || body.Events[1].EventType != "claim_aborted" {
		t.Fatalf("事件必须按真实时刻排序: %+v", body.Events)
	}
}
