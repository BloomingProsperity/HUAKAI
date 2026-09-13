package accountbulkhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/provideraccountrecovery"
)

// fakeStore 用内存账号表模拟逐项事务:记录每次调用的参数,便于断言范围与 dry-run。
type fakeStore struct {
	mu        sync.Mutex
	accounts  map[int64]fakeAccount // key: id;TenantID 用于模拟他租户
	channels  map[int64]int64       // channel id → tenant
	fieldArgs []FieldUpdate
	moveArgs  []ChannelMove
	audits    []BatchAudit
	failIDs   map[int64]bool // 模拟数据库错误
}

type fakeAccount struct {
	TenantID     int64
	Enabled      bool
	Priority     int32
	StaticWeight int32
	ChannelID    int64
	HighRiskMove bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		accounts: map[int64]fakeAccount{
			1: {TenantID: 7, Enabled: true, Priority: 10, StaticWeight: 1, ChannelID: 100},
			2: {TenantID: 7, Enabled: false, Priority: 5, StaticWeight: 1, ChannelID: 100},
			3: {TenantID: 8, Enabled: true, Priority: 1, StaticWeight: 1, ChannelID: 200}, // 他租户
			4: {TenantID: 7, Enabled: true, Priority: 1, StaticWeight: 1, ChannelID: 100, HighRiskMove: true},
		},
		channels: map[int64]int64{100: 7, 101: 7, 200: 8},
		failIDs:  map[int64]bool{},
	}
}

func (f *fakeStore) ApplyFieldUpdate(_ context.Context, arg FieldUpdate) (ItemResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fieldArgs = append(f.fieldArgs, arg)
	if f.failIDs[arg.AccountID] {
		return ItemResult{}, errors.New("db down")
	}
	acct, ok := f.accounts[arg.AccountID]
	if !ok || acct.TenantID != arg.TenantID {
		return notFound(arg.AccountID), nil
	}
	if (arg.Enabled == nil || *arg.Enabled == acct.Enabled) && (arg.Priority == nil || *arg.Priority == acct.Priority) && (arg.StaticWeight == nil || *arg.StaticWeight == acct.StaticWeight) {
		return ItemResult{ID: arg.AccountID, Status: StatusSkipped, Code: CodeAlreadyInDesiredState}, nil
	}
	if arg.DryRun {
		return ItemResult{ID: arg.AccountID, Status: StatusWouldApply}, nil
	}
	if arg.Enabled != nil {
		acct.Enabled = *arg.Enabled
	}
	if arg.Priority != nil {
		acct.Priority = *arg.Priority
	}
	if arg.StaticWeight != nil {
		acct.StaticWeight = *arg.StaticWeight
	}
	f.accounts[arg.AccountID] = acct
	return ItemResult{ID: arg.AccountID, Status: StatusSucceeded}, nil
}

func (f *fakeStore) MoveChannel(_ context.Context, arg ChannelMove) (ItemResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moveArgs = append(f.moveArgs, arg)
	acct, ok := f.accounts[arg.AccountID]
	if !ok || acct.TenantID != arg.TenantID {
		return notFound(arg.AccountID), nil
	}
	if acct.ChannelID == arg.TargetChannelID {
		return ItemResult{ID: arg.AccountID, Status: StatusSkipped, Code: CodeInvalidTargetSameChannel}, nil
	}
	if tenant, ok := f.channels[arg.TargetChannelID]; !ok || tenant != arg.TenantID {
		return ItemResult{ID: arg.AccountID, Status: StatusFailed, Code: CodeChannelNotFound}, nil
	}
	if acct.HighRiskMove && !arg.ConfirmMixedRisk {
		return ItemResult{ID: arg.AccountID, Status: StatusFailed, Code: CodeMixedRiskConfirmRequired, Detail: map[string]any{"mixed_risk": "high"}}, nil
	}
	if arg.DryRun {
		return ItemResult{ID: arg.AccountID, Status: StatusWouldApply}, nil
	}
	acct.ChannelID = arg.TargetChannelID
	f.accounts[arg.AccountID] = acct
	return ItemResult{ID: arg.AccountID, Status: StatusSucceeded}, nil
}

func (f *fakeStore) InsertBatchAudit(_ context.Context, arg BatchAudit) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, arg)
	return int64(len(f.audits)), nil
}

type fakeRecovery struct {
	mu    sync.Mutex
	calls []provideraccountrecovery.ClearRateLimitInput
	mode  map[int64]string // "partial" | "missing" | "error"
}

func (f *fakeRecovery) ClearRateLimit(_ context.Context, in provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error) {
	return f.respond(in)
}

func (f *fakeRecovery) RecoverAccountState(_ context.Context, in provideraccountrecovery.RecoverAccountInput) (provideraccountrecovery.RecoverAccountResult, error) {
	return f.respond(in)
}

func (f *fakeRecovery) respond(in provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	switch f.mode[in.AccountID] {
	case "partial":
		return provideraccountrecovery.ClearRateLimitResult{}, provideraccountrecovery.ErrPartialRecovery
	case "missing":
		return provideraccountrecovery.ClearRateLimitResult{}, pgx.ErrNoRows
	case "error":
		return provideraccountrecovery.ClearRateLimitResult{}, errors.New("boom")
	}
	return provideraccountrecovery.ClearRateLimitResult{ChannelChanged: true}, nil
}

type fakeRefresher struct {
	outcomes   map[int64]credentialworker.RefreshNowOutcome
	err        map[int64]error
	inflight   atomic.Int32
	maxSeen    atomic.Int32
	delay      time.Duration
	calls      atomic.Int32
	blockUntil context.Context
}

func (f *fakeRefresher) RefreshAccountNow(ctx context.Context, _ int64, accountID int64) (credentialworker.RefreshNowOutcome, error) {
	f.calls.Add(1)
	cur := f.inflight.Add(1)
	for {
		prev := f.maxSeen.Load()
		if cur <= prev || f.maxSeen.CompareAndSwap(prev, cur) {
			break
		}
	}
	defer f.inflight.Add(-1)
	if f.blockUntil != nil {
		<-f.blockUntil.Done()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
		}
	}
	if err, ok := f.err[accountID]; ok {
		return credentialworker.RefreshNowOutcome{}, err
	}
	if out, ok := f.outcomes[accountID]; ok {
		return out, nil
	}
	return credentialworker.RefreshNowOutcome{Status: credentialworker.RefreshNowRefreshed}, nil
}

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.ident, a.err
}

func tenantOperator(tenant int64) admin.AdminIdentity {
	return admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: tenant, UserID: 2}
}

func call(t *testing.T, d Deps, target string, body string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/admin/v1/provider-accounts", func(r chi.Router) { MountRoutes(r, d) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	var resp Response
	if rec.Code == http.StatusOK || rec.Code == http.StatusMultiStatus {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("状态=%d body=%s,期望 %d %s", rec.Code, rec.Body.String(), status, code)
	}
}

func byID(results []ItemResult, id int64) ItemResult {
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	return ItemResult{}
}

// 启停:重复 ID 去重;已达期望态 skipped;他租户 / 不存在同为 not_found;成功项写入;
// 存在失败即 207 + partial_success;批次日志含计数;租户管理员锁本租户。
func TestSetEnabledPartialSuccessAndScope(t *testing.T) {
	store := newFakeStore()
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1,1,2,3,999],"action":"set_enabled","enabled":false,"reason":"维护"}`)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("存在失败项应 207: %d %s", rec.Code, rec.Body.String())
	}
	if resp.Total != 4 || resp.DuplicateIDsRemoved != 1 || resp.Succeeded != 1 || resp.Skipped != 1 || resp.Failed != 2 || !resp.PartialSuccess || !resp.SucceededNotRolledBack {
		t.Fatalf("计数不符: %+v", resp)
	}
	if byID(resp.Results, 1).Status != StatusSucceeded || byID(resp.Results, 2).Code != CodeAlreadyInDesiredState {
		t.Fatalf("逐项结果不符: %+v", resp.Results)
	}
	other, missing := byID(resp.Results, 3), byID(resp.Results, 999)
	if other.Code != CodeNotFound || missing.Code != CodeNotFound || other.Message != missing.Message || other.Status != missing.Status {
		t.Fatalf("他租户与不存在必须同一结果: %+v vs %+v", other, missing)
	}
	if store.accounts[1].Enabled || store.accounts[3].Enabled != true {
		t.Fatalf("成功项应写入、他租户不得被改: %+v", store.accounts)
	}
	for _, arg := range store.fieldArgs {
		if arg.TenantID != 7 || arg.ActorRole != admin.RoleTenantOperator || arg.Reason != "维护" {
			t.Fatalf("逐项事务必须带认证租户、角色与原因: %+v", arg)
		}
	}
	if len(store.audits) != 1 || store.audits[0].Action != ActionSetEnabled || store.audits[0].Summary["succeeded"] != 1 || store.audits[0].Summary["failed"] != 2 || store.audits[0].Summary["confirm_mixed_risk"] != false || resp.BatchAuditID == nil {
		t.Fatalf("批次日志不符: %+v", store.audits)
	}
	if len(resp.SucceededIDs) != 1 || resp.SucceededIDs[0] != 1 || len(resp.FailedIDs) != 2 || resp.FailedIDs[0] != 3 || resp.FailedIDs[1] != 999 {
		t.Fatalf("成功/失败 ID 列表不符: %+v", resp)
	}
}

// 全部成功 → 200 且 partial_success=false;dry-run 不写、不记批次日志、结果为 would_apply。
func TestAllSucceededAndDryRun(t *testing.T) {
	store := newFakeStore()
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"set_priority","priority":99}`)
	if rec.Code != http.StatusOK || resp.PartialSuccess || resp.Succeeded != 1 || store.accounts[1].Priority != 99 {
		t.Fatalf("全部成功应 200: %d %+v", rec.Code, resp)
	}
	rec, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[2],"action":"set_priority","priority":42,"dry_run":true}`)
	if rec.Code != http.StatusOK || !resp.DryRun || byID(resp.Results, 2).Status != StatusWouldApply || store.accounts[2].Priority != 5 || resp.BatchAuditID != nil || len(store.audits) != 1 {
		t.Fatalf("dry-run 不得写入或记批次日志: %d %+v audits=%d", rec.Code, resp, len(store.audits))
	}
}

// 校验:空 ids、非正数、非法动作、缺参数、超上限、未知字段、原因过长 → 400;部署者缺 tenant_id → 400;
// 租户管理员显式他人租户 → 403;无身份 → 401;Store 缺失 → 503。任何 4xx 都不得触发逐项写。
func TestValidationAndScopeErrors(t *testing.T) {
	store := newFakeStore()
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store, MaxIDs: 3}
	cases := []struct {
		body, code string
	}{
		{`{"ids":[],"action":"set_enabled","enabled":true}`, "ids_required"},
		{`{"ids":[0],"action":"set_enabled","enabled":true}`, "invalid_id"},
		{`{"ids":[1],"action":"nuke"}`, "invalid_action"},
		{`{"ids":[1],"action":"set_enabled"}`, "enabled_required"},
		{`{"ids":[1],"action":"set_priority"}`, "priority_required"},
		{`{"ids":[1],"action":"set_static_weight","static_weight":-1}`, "static_weight_required"},
		{`{"ids":[1],"action":"move_channel"}`, "channel_id_required"},
		{`{"ids":[1,2,3,4],"action":"set_enabled","enabled":true}`, "bulk_target_limit_exceeded"},
		{`{"ids":[1],"action":"set_enabled","enabled":true,"bogus":1}`, "invalid_json"},
		{`{"ids":[1],"action":"set_enabled","enabled":true,"reason":"` + strings.Repeat("长", 513) + `"}`, "reason_too_long"},
		{`{"ids":[1],"action":"set_enabled","enabled":false}`, "reason_required"},
		{`{"ids":[1],"action":"clear_rate_limit"}`, "reason_required"},
	}
	for _, tc := range cases {
		rec, _ := call(t, deps, "/admin/v1/provider-accounts/bulk", tc.body)
		expectError(t, rec, http.StatusBadRequest, tc.code)
	}
	if len(store.fieldArgs) != 0 {
		t.Fatal("校验失败不得触发逐项写")
	}
	rec, _ := call(t, deps, "/admin/v1/provider-accounts/bulk?tenant_id=8", `{"ids":[1],"action":"set_enabled","enabled":true}`)
	expectError(t, rec, http.StatusForbidden, "admin_forbidden")
	platform := Deps{Auth: authStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: store}
	rec, _ = call(t, platform, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"set_enabled","enabled":true}`)
	expectError(t, rec, http.StatusBadRequest, "tenant_id_required")
	rec, resp := call(t, platform, "/admin/v1/provider-accounts/bulk?tenant_id=8", `{"ids":[3],"action":"set_enabled","enabled":false,"reason":"跨租户维护"}`)
	if rec.Code != http.StatusOK || resp.TenantID != 8 || store.accounts[3].Enabled {
		t.Fatalf("部署者显式租户应放行并按该租户写: %d %+v", rec.Code, resp)
	}
	rec, _ = call(t, Deps{Auth: authStub{ident: admin.AdminIdentity{}}, Store: store}, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"set_enabled","enabled":true}`)
	expectError(t, rec, http.StatusUnauthorized, "admin_unauthorized")
	rec, _ = call(t, Deps{Auth: authStub{ident: tenantOperator(7)}}, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"set_enabled","enabled":true}`)
	expectError(t, rec, http.StatusServiceUnavailable, "gateway_not_configured")
	if len(store.fieldArgs) != 1 {
		t.Fatalf("只有部署者显式租户的那次应触库: %d", len(store.fieldArgs))
	}
}

// 数据库错误只影响该项(store_error、可重试),其他项照常;超上限用刷新专用上限。
func TestStoreErrorIsolatedPerItem(t *testing.T) {
	store := newFakeStore()
	store.failIDs[2] = true
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1,2],"action":"set_static_weight","static_weight":7}`)
	if rec.Code != http.StatusMultiStatus || byID(resp.Results, 1).Status != StatusSucceeded || byID(resp.Results, 2).Code != CodeUpstreamStoreError || !byID(resp.Results, 2).Retryable {
		t.Fatalf("数据库错误应隔离到单项: %d %+v", rec.Code, resp.Results)
	}
	rec, _ = call(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store, Refresher: &fakeRefresher{}, MaxRefreshIDs: 2}, "/admin/v1/provider-accounts/bulk", `{"ids":[1,2,4],"action":"refresh_credential"}`)
	expectError(t, rec, http.StatusBadRequest, "bulk_target_limit_exceeded")
}

// 清限流 / 恢复:复用同一原语;部分恢复 → partial_recovery 可重试;no rows → not_found;
// 其他错误 → store_error;依赖未装配 → dependency_not_configured;dry-run 不调用原语。
func TestRecoveryActions(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecovery{mode: map[int64]string{2: "partial", 3: "missing", 4: "error"}}
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store, Recovery: rec}
	code, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1,2,3,4],"action":"clear_rate_limit","reason":"限流误伤"}`)
	if code.Code != http.StatusMultiStatus || byID(resp.Results, 1).Status != StatusSucceeded ||
		byID(resp.Results, 2).Code != CodePartialRecovery || !byID(resp.Results, 2).Retryable ||
		byID(resp.Results, 3).Code != CodeNotFound || byID(resp.Results, 4).Code != CodeUpstreamStoreError {
		t.Fatalf("恢复类逐项结果不符: %+v", resp.Results)
	}
	for _, in := range rec.calls {
		if in.TenantID != 7 || in.ActorRole != admin.RoleTenantOperator || in.Reason != "限流误伤" {
			t.Fatalf("恢复原语必须带认证租户、角色与原因: %+v", in)
		}
	}
	before := len(rec.calls)
	if _, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"recover","dry_run":true,"reason":"恢复预览"}`); len(rec.calls) != before || byID(resp.Results, 1).Status != StatusWouldApply {
		t.Fatal("dry-run 不得调用恢复原语")
	}
	if _, resp = call(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store}, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"recover","reason":"完整恢复"}`); byID(resp.Results, 1).Code != CodeDependencyNotConfigured {
		t.Fatalf("依赖未装配应逐项报告: %+v", resp.Results)
	}
	if _, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"recover","reason":"完整恢复"}`); byID(resp.Results, 1).Status != StatusSucceeded || byID(resp.Results, 1).Detail["channel_changed"] != true {
		t.Fatalf("完整恢复应成功并带渠道变化: %+v", resp.Results)
	}
}

// 立刻刷新:结论映射(refreshed → succeeded;in_progress / deferred → deferred 可重试;not_applicable → skipped;
// not_found;failed);并发受工人上限约束;基础设施错误 → store_error。
func TestRefreshOutcomeMappingAndWorkerLimit(t *testing.T) {
	store := newFakeStore()
	refresher := &fakeRefresher{
		delay: 20 * time.Millisecond,
		outcomes: map[int64]credentialworker.RefreshNowOutcome{
			11: {Status: credentialworker.RefreshNowRefreshed},
			12: {Status: credentialworker.RefreshNowInProgress, Scope: "account", Detail: "refresh_lock_held"},
			13: {Status: credentialworker.RefreshNowDeferred, Scope: "global", Detail: "storm_budget_exhausted"},
			14: {Status: credentialworker.RefreshNowNotApplicable, Scope: "credential_mode", Detail: "static_credential"},
			15: {Status: credentialworker.RefreshNowNotFound},
			16: {Status: credentialworker.RefreshNowFailed, Scope: "refresh", Detail: "invalid_grant"},
		},
		err: map[int64]error{17: errors.New("db down")},
	}
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store, Refresher: refresher, RefreshWorkers: 2}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[11,12,13,14,15,16,17],"action":"refresh_credential"}`)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("存在推迟/失败应 207: %d", rec.Code)
	}
	want := map[int64][2]string{
		11: {StatusSucceeded, ""}, 12: {StatusDeferred, CodeRefreshInProgress}, 13: {StatusDeferred, CodeRefreshDeferred},
		14: {StatusSkipped, CodeRefreshNotApplicable}, 15: {StatusFailed, CodeNotFound}, 16: {StatusFailed, CodeRefreshFailed}, 17: {StatusFailed, CodeUpstreamStoreError},
	}
	for id, w := range want {
		got := byID(resp.Results, id)
		if got.Status != w[0] || got.Code != w[1] {
			t.Fatalf("账号 %d: %+v,期望 %v", id, got, w)
		}
	}
	if resp.Succeeded != 1 || resp.Deferred != 2 || resp.Skipped != 1 || resp.Failed != 3 || !resp.PartialSuccess {
		t.Fatalf("计数不符: %+v", resp)
	}
	if got := refresher.maxSeen.Load(); got > 2 {
		t.Fatalf("并发刷新不得超过工人上限 2,实际 %d", got)
	}
	if byID(resp.Results, 16).Detail["outcome"] != "invalid_grant" || byID(resp.Results, 12).Retryable != true {
		t.Fatalf("刷新明细/可重试标记不符: %+v", resp.Results)
	}
}

// 批次超时:超时后未开始的项标 cancelled/batch_timeout 并计入 failed_ids,不再调用刷新。
func TestBatchTimeoutCancelsUnstartedItems(t *testing.T) {
	store := newFakeStore()
	refresher := &fakeRefresher{delay: 200 * time.Millisecond}
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store, Refresher: refresher, RefreshWorkers: 1, BatchTimeout: 50 * time.Millisecond}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[21,22,23,24],"action":"refresh_credential"}`)
	if rec.Code != http.StatusMultiStatus || resp.Cancelled == 0 || len(resp.FailedIDs) != resp.Cancelled+resp.Failed {
		t.Fatalf("超时应把未开始项标 cancelled 并列入 failed_ids: %d %+v", rec.Code, resp)
	}
	if calls := int(refresher.calls.Load()); calls+resp.Cancelled != 4 || calls > 2 {
		t.Fatalf("超时后不得继续调用刷新: calls=%d cancelled=%d", calls, resp.Cancelled)
	}
	for _, r := range resp.Results {
		if r.Status == StatusCancelled && (r.Code != CodeBatchTimeout || !r.Retryable) {
			t.Fatalf("取消项结果码不符: %+v", r)
		}
	}
}

// 改渠道:同租户校验、混合风险确认、已在目标渠道 skipped、他租户渠道 channel_not_found。
func TestMoveChannel(t *testing.T) {
	store := newFakeStore()
	deps := Deps{Auth: authStub{ident: tenantOperator(7)}, Store: store}
	rec, resp := call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1,2,4],"action":"move_channel","channel_id":101}`)
	if rec.Code != http.StatusMultiStatus || byID(resp.Results, 1).Status != StatusSucceeded || byID(resp.Results, 4).Code != CodeMixedRiskConfirmRequired || store.accounts[1].ChannelID != 101 || store.accounts[4].ChannelID != 100 {
		t.Fatalf("改渠道结果不符: %+v", resp.Results)
	}
	_, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[4],"action":"move_channel","channel_id":101,"confirm_mixed_risk":true}`)
	if byID(resp.Results, 4).Status != StatusSucceeded || store.accounts[4].ChannelID != 101 {
		t.Fatalf("确认混合风险后应写入: %+v", resp.Results)
	}
	_, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1,2],"action":"move_channel","channel_id":200}`)
	if byID(resp.Results, 1).Code != CodeChannelNotFound || byID(resp.Results, 2).Code != CodeChannelNotFound {
		t.Fatalf("他租户渠道必须拒绝: %+v", resp.Results)
	}
	_, resp = call(t, deps, "/admin/v1/provider-accounts/bulk", `{"ids":[1],"action":"move_channel","channel_id":101}`)
	if byID(resp.Results, 1).Code != CodeInvalidTargetSameChannel {
		t.Fatalf("已在目标渠道应 skipped: %+v", resp.Results)
	}
}
