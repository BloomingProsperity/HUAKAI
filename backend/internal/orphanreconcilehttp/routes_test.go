package orphanreconcilehttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

// fakeStore 是不碰 DB 的孤儿存储替身。记录 ReconcileOrphan 被调用的参数,并按 reconcile_status
// 状态门模拟"已对账孤儿再追扣为 no-op"——好让幂等 / 双扣变异在无 DB 时也能让测试变红。
type fakeStore struct {
	pending []mediatask.OrphanRecord
	// 每个孤儿当前状态(模拟 reconcile_status)。
	statuses map[int64]string
	// 模拟余额扣减次数:每次真追扣(从 pending→reconciled 且 backCharge)+1。
	captureCalls int
	listTenantID int64 // 记录列表查询传入的租户 scope,用于 RBAC scope 断言
	attachCalls  int
	releaseCalls int
}

func newFakeStore(recs ...mediatask.OrphanRecord) *fakeStore {
	s := &fakeStore{pending: recs, statuses: map[int64]string{}}
	for _, r := range recs {
		s.statuses[r.ID] = "pending"
	}
	return s
}

func (s *fakeStore) ListPendingOrphans(_ context.Context, tenantID int64, _ int) ([]mediatask.OrphanRecord, error) {
	s.listTenantID = tenantID
	out := make([]mediatask.OrphanRecord, 0, len(s.pending))
	for _, r := range s.pending {
		// scope 收窄:tenantID>0 时只返回该租户;<=0 全返回(模拟生产 SQL 行为)。
		if tenantID > 0 && r.TenantID != tenantID {
			continue
		}
		if s.statuses[r.ID] == "pending" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *fakeStore) ReconcileOrphan(ctx context.Context, orphanID int64, status string, backCharge bool, _ time.Time, audit mediatask.OrphanReconcileAuditHook) (mediatask.OrphanReconcileResult, bool, error) {
	switch status {
	case "reconciled", "cancelled", "ignored":
	default:
		return mediatask.OrphanReconcileResult{}, false, mediatask.ErrInvalidOrphanStatus
	}
	if backCharge && status != "reconciled" {
		return mediatask.OrphanReconcileResult{}, false, mediatask.ErrInvalidOrphanStatus
	}
	// 状态门(命门):已是终态的孤儿再对账 → no-op,绝不进 capture。
	if s.statuses[orphanID] != "pending" {
		return mediatask.OrphanReconcileResult{}, false, nil
	}
	var rec mediatask.OrphanRecord
	for _, r := range s.pending {
		if r.ID == orphanID {
			rec = r
		}
	}
	result := mediatask.OrphanReconcileResult{
		OrphanID: orphanID, TaskID: rec.TaskID, TenantID: rec.TenantID, UserID: rec.UserID, Status: status,
	}
	if backCharge {
		s.captureCalls++ // 模拟一次真扣款
		result.BackCharged = true
		result.CapturedCents = 123
	}
	// 注:audit hook 的真正执行(写 admin_audit_events + 越权回滚)是 DB-tx 内行为,由
	// 集成测试 TestReconcileOrphanAuditHookRollsBackOnError 覆盖;此处 fake 无 tx,故不调用
	// hook 以免对 nil tx 写库 panic。越权守卫逻辑由 TestBuildAuditHookForbidsCrossTenant 单测。
	_ = audit
	_ = ctx
	s.statuses[orphanID] = status
	return result, true, nil
}

func (s *fakeStore) AttachUnknownSubmission(
	ctx context.Context,
	orphanID int64,
	providerTaskID string,
	_ time.Time,
	access mediatask.SubmissionRecoveryAccessHook,
	_ mediatask.SubmissionRecoveryAuditHook,
) (mediatask.SubmissionRecoveryResult, bool, error) {
	rec, ok := s.record(orphanID)
	if !ok {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrNotFound
	}
	result := mediatask.SubmissionRecoveryResult{
		OrphanID: orphanID, TaskID: rec.TaskID, TenantID: rec.TenantID, UserID: rec.UserID,
		Provider: rec.Provider, ProviderTaskID: providerTaskID,
		TaskStatus: mediatask.StatusInProgress, OrphanStatus: "reconciled",
		EstimatedCents: rec.EstimatedCents,
	}
	if access == nil {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrSubmissionAccessNotConfigured
	}
	if err := access(ctx, result); err != nil {
		return mediatask.SubmissionRecoveryResult{}, false, err
	}
	if rec.OrphanKind != "submission_unknown" || s.statuses[orphanID] != "pending" {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrSubmissionNotUnknown
	}
	s.attachCalls++
	s.statuses[orphanID] = "reconciled"
	return result, true, nil
}

func (s *fakeStore) RequestUnknownSubmissionRelease(
	ctx context.Context,
	orphanID int64,
	_ time.Time,
	access mediatask.SubmissionRecoveryAccessHook,
	_ mediatask.SubmissionRecoveryAuditHook,
) (mediatask.SubmissionRecoveryResult, bool, error) {
	rec, ok := s.record(orphanID)
	if !ok {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrNotFound
	}
	result := mediatask.SubmissionRecoveryResult{
		OrphanID: orphanID, TaskID: rec.TaskID, TenantID: rec.TenantID, UserID: rec.UserID,
		Provider: rec.Provider, TaskStatus: mediatask.StatusSubmissionReleasing,
		OrphanStatus: "release_requested", EstimatedCents: rec.EstimatedCents,
	}
	if access == nil {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrSubmissionAccessNotConfigured
	}
	if err := access(ctx, result); err != nil {
		return mediatask.SubmissionRecoveryResult{}, false, err
	}
	if rec.OrphanKind != "submission_unknown" {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrSubmissionNotUnknown
	}
	if s.statuses[orphanID] == "release_requested" {
		return result, false, nil
	}
	if s.statuses[orphanID] != "pending" {
		return mediatask.SubmissionRecoveryResult{}, false, mediatask.ErrSubmissionNotUnknown
	}
	s.releaseCalls++
	s.statuses[orphanID] = "release_requested"
	return result, true, nil
}

func (s *fakeStore) record(orphanID int64) (mediatask.OrphanRecord, bool) {
	for _, rec := range s.pending {
		if rec.ID == orphanID {
			return rec, true
		}
	}
	return mediatask.OrphanRecord{}, false
}

type fakeAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (f fakeAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return f.ident, f.err
}

func newRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/admin/v1/media-task-orphans", NewListHandler(d))
	r.Post("/admin/v1/media-task-orphans/{id}/reconcile", NewReconcileHandler(d))
	r.Post("/admin/v1/media-task-orphans/{id}/attach", NewAttachHandler(d))
	r.Post("/admin/v1/media-task-orphans/{id}/confirm-not-accepted", NewConfirmNotAcceptedHandler(d))
	return r
}

func sampleOrphans() []mediatask.OrphanRecord {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	return []mediatask.OrphanRecord{
		{ID: 1, TaskID: 101, TenantID: 7, UserID: 42, Provider: "midjourney", ProviderTaskID: "up-1", ReconcileStatus: "pending", ObservedAt: now},
		{ID: 2, TaskID: 102, TenantID: 9, UserID: 43, Provider: "suno", ProviderTaskID: "up-2", ReconcileStatus: "pending", ObservedAt: now},
	}
}

// TestListVisualizesPendingOrphans:platform_admin 列出 pending 孤儿(可视化 A)。
// 判别:断言返回了具体 task_id / provider / provider_task_id,不是空壳——若 toItem 漏映射字段则 RED。
func TestListVisualizesPendingOrphans(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store, PlatformTenantID: 7}
	rr := httptest.NewRecorder()
	newRouter(d).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/v1/media-task-orphans", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("len(items)=%d want 2", len(resp.Items))
	}
	if resp.Items[0].TaskID != 101 || resp.Items[0].ProviderTaskID != "up-1" || resp.Items[0].Provider != "midjourney" {
		t.Fatalf("item[0] 字段错 %+v", resp.Items[0])
	}
	// platform_admin 不带 tenant_id → 全局扫(listTenantID==0)。
	if store.listTenantID != 0 {
		t.Fatalf("platform_admin 默认应全局扫 tenantID=0, got %d", store.listTenantID)
	}
}

// TestListTenantOperatorScopedToOwnTenant:tenant_operator 强制限自己租户(RBAC scope)。
// 判别:断言 store 收到的 tenantID == 该 operator 的 ScopeTenantID。若 resolveListScope 误把
// operator 当全局扫(传 0)→ RED(防越权看他租户孤儿)。
func TestListTenantOperatorScopedToOwnTenant(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 9}}, Store: store}
	rr := httptest.NewRecorder()
	newRouter(d).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/v1/media-task-orphans", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if store.listTenantID != 9 {
		t.Fatalf("tenant_operator scope 应为 9, store 收到 %d", store.listTenantID)
	}
	var resp listResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Items) != 1 || resp.Items[0].TenantID != 9 {
		t.Fatalf("operator 只应看到自己租户的孤儿, got %+v", resp.Items)
	}
}

// TestUnauthorizedRejected:鉴权失败(B 变异)——Auth.Resolve 报 unauthorized → 401,不进 store。
func TestUnauthorizedRejected(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{err: admin.ErrAdminUnauthorized}, Store: store}
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/admin/v1/media-task-orphans"},
		{http.MethodPost, "/admin/v1/media-task-orphans/1/reconcile"},
		{http.MethodPost, "/admin/v1/media-task-orphans/1/attach"},
		{http.MethodPost, "/admin/v1/media-task-orphans/1/confirm-not-accepted"},
	} {
		rr := httptest.NewRecorder()
		newRouter(d).ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d want 401", tc.method, tc.path, rr.Code)
		}
	}
	if store.captureCalls != 0 {
		t.Fatalf("鉴权失败不应触发任何追扣, captureCalls=%d", store.captureCalls)
	}
}

func TestAttachUnknownSubmissionRequiresStrictEvidenceAndReturnsRecoveryState(t *testing.T) {
	rec := mediatask.OrphanRecord{
		ID: 11, TaskID: 111, TenantID: 7, UserID: 42, Provider: "grok_video",
		OrphanKind: "submission_unknown", TaskStatus: mediatask.StatusSubmissionUnknown,
		EstimatedCents: 123, ReconcileStatus: "pending", ObservedAt: time.Now().UTC(),
	}
	store := newFakeStore(rec)
	d := Deps{
		Auth: fakeAuth{ident: admin.AdminIdentity{
			Role: admin.RoleTenantOperator, ScopeTenantID: 7,
		}},
		Store: store,
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/media-task-orphans/11/attach",
		strings.NewReader(`{"provider_task_id":"up-111","reason":"供应商后台已找到","evidence":"工单-111"}`),
	)
	newRouter(d).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response submissionRecoveryResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Advanced || response.TaskStatus != string(mediatask.StatusInProgress) ||
		response.OrphanStatus != "reconciled" || response.ProviderTaskID != "up-111" {
		t.Fatalf("补录响应不完整: %+v", response)
	}
	if store.attachCalls != 1 || store.releaseCalls != 0 {
		t.Fatalf("attach/release calls=%d/%d", store.attachCalls, store.releaseCalls)
	}

	strictStore := newFakeStore(rec)
	strictDeps := d
	strictDeps.Store = strictStore
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/media-task-orphans/11/attach",
		strings.NewReader(`{"provider_task_id":"up-111","reason":"r","evidence":"e","tenant_id":9}`),
	)
	newRouter(strictDeps).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || strictStore.attachCalls != 0 {
		t.Fatalf("额外字段必须在触发存储前拒绝: status=%d calls=%d body=%s",
			rr.Code, strictStore.attachCalls, rr.Body.String())
	}
}

func TestConfirmNotAcceptedRequiresEvidenceAndQueuesReleaseIdempotently(t *testing.T) {
	rec := mediatask.OrphanRecord{
		ID: 12, TaskID: 112, TenantID: 7, UserID: 42, Provider: "gemini_video",
		OrphanKind: "submission_unknown", TaskStatus: mediatask.StatusSubmissionUnknown,
		EstimatedCents: 456, ReconcileStatus: "pending", ObservedAt: time.Now().UTC(),
	}
	store := newFakeStore(rec)
	d := Deps{
		Auth: fakeAuth{ident: admin.AdminIdentity{
			Role: admin.RoleTenantOperator, ScopeTenantID: 7,
		}},
		Store: store,
	}

	for index := 0; index < 2; index++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/admin/v1/media-task-orphans/12/confirm-not-accepted",
			strings.NewReader(`{"reason":"供应商确认未受理","evidence":"工单-112"}`),
		)
		newRouter(d).ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("第 %d 次 status=%d body=%s", index+1, rr.Code, rr.Body.String())
		}
		var response submissionRecoveryResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Advanced != (index == 0) ||
			response.TaskStatus != string(mediatask.StatusSubmissionReleasing) ||
			response.OrphanStatus != "release_requested" {
			t.Fatalf("第 %d 次响应=%+v", index+1, response)
		}
	}
	if store.releaseCalls != 1 || store.attachCalls != 0 {
		t.Fatalf("重复裁决触发次数不幂等: release=%d attach=%d",
			store.releaseCalls, store.attachCalls)
	}

	missingEvidenceStore := newFakeStore(rec)
	missingEvidenceDeps := d
	missingEvidenceDeps.Store = missingEvidenceStore
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/media-task-orphans/12/confirm-not-accepted",
		strings.NewReader(`{"reason":"只有理由"}`),
	)
	newRouter(missingEvidenceDeps).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || missingEvidenceStore.releaseCalls != 0 {
		t.Fatalf("缺证据必须拒绝: status=%d calls=%d",
			rr.Code, missingEvidenceStore.releaseCalls)
	}

	secretEvidenceStore := newFakeStore(rec)
	secretEvidenceDeps := d
	secretEvidenceDeps.Store = secretEvidenceStore
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/media-task-orphans/12/confirm-not-accepted",
		strings.NewReader(`{"reason":"供应商确认未受理","evidence":"sk-ant-fakekey"}`),
	)
	newRouter(secretEvidenceDeps).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || secretEvidenceStore.releaseCalls != 0 {
		t.Fatalf("疑似秘密不得进入日志事务: status=%d calls=%d body=%s",
			rr.Code, secretEvidenceStore.releaseCalls, rr.Body.String())
	}
}

func TestSubmissionRecoveryAccessForbidsCrossTenantBeforeStateDisclosure(t *testing.T) {
	ident := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}
	hook := buildSubmissionRecoveryAccess(ident, 0)
	err := hook(context.Background(), mediatask.SubmissionRecoveryResult{
		OrphanID: 13, TaskID: 113, TenantID: 9, UserID: 43,
		Provider: "grok_video", ProviderTaskID: "up-113",
		TaskStatus: mediatask.StatusInProgress, OrphanStatus: "reconciled",
	})
	if !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("跨租户恢复必须在状态判断前拒绝: %v", err)
	}
}

// TestNonAdminRoleForbidden:已认证但非 admin 角色 → 403(RBAC)。
func TestNonAdminRoleForbidden(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: "user"}}, Store: store}
	rr := httptest.NewRecorder()
	newRouter(d).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/v1/media-task-orphans/1/reconcile", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("非 admin 角色 status=%d want 403", rr.Code)
	}
	if store.captureCalls != 0 {
		t.Fatalf("非 admin 不应触发追扣, captureCalls=%d", store.captureCalls)
	}
}

// TestReconcileMarkOnlyDefaultNoCharge:默认动作(不带 back_charge)只标记不扣钱(Manual-First)。
// 判别:断言 captureCalls 始终为 0。若 handler 把 back_charge 默认成 true → RED。
func TestReconcileMarkOnlyDefaultNoCharge(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store, PlatformTenantID: 7}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/media-task-orphans/1/reconcile",
		strings.NewReader(`{"status":"reconciled"}`))
	newRouter(d).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp reconcileResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.Advanced || resp.BackCharged {
		t.Fatalf("默认对账应仅标记 advanced=true backcharged=false, got %+v", resp)
	}
	if store.captureCalls != 0 {
		t.Fatalf("默认动作不应追扣, captureCalls=%d (Manual-First 违规)", store.captureCalls)
	}
}

// TestReconcileBackChargeIdempotentNoDoubleCharge:命门 C(http 层)——同一孤儿带 back_charge
// 对账两次,真扣款只发生一次(captureCalls==1)。
//
// 变异:把 fakeStore.ReconcileOrphan 的状态门去掉(已 reconciled 也允许再 capture)→ 第二次
// captureCalls 变 2,本测试断言 captureCalls==1 RED。这就是防双扣的判别。
func TestReconcileBackChargeIdempotentNoDoubleCharge(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store, PlatformTenantID: 7}
	body := `{"status":"reconciled","back_charge":true}`

	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/v1/media-task-orphans/1/reconcile", strings.NewReader(body))
		newRouter(d).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("第 %d 次 status=%d body=%s", i+1, rr.Code, rr.Body.String())
		}
	}
	if store.captureCalls != 1 {
		t.Fatalf("两次追扣后 captureCalls=%d want 1(双扣亏钱)", store.captureCalls)
	}
}

// TestBuildAuditHookForbidsCrossTenant:租户越权守卫(RBAC)——tenant_operator 对账不属其
// 租户的孤儿，audit hook 在触碰任何 DB 前就返回 ErrAdminForbidden(回滚整笔)。
// 判别:若去掉所属租户经营边界 → hook 会继续往下写 admin_audit_events(对 nil tx
// panic 或越权写库)，本测试断言它返回 ErrAdminForbidden 后转红。
func TestBuildAuditHookForbidsCrossTenant(t *testing.T) {
	ident := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}
	hook := buildAuditHook(ident, 1, reconcileRequest{Status: "reconciled"}, "req-x")
	// 孤儿属租户 9,operator 限租户 7 → 越权,必须在写库前拒绝。
	err := hook(context.Background(), nil /*tx 不会被触碰*/, mediatask.OrphanReconcileResult{
		OrphanID: 2, TaskID: 102, TenantID: 9, UserID: 43, Status: "reconciled",
	})
	if !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("越权应返回 ErrAdminForbidden, got %v", err)
	}
}

func TestPlatformAdminCannotReconcileDownstreamTenant(t *testing.T) {
	ident := admin.AdminIdentity{Role: admin.RolePlatformAdmin}
	if err := authorizeOrphanMutation(ident, 7, 9); !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("部署者越级处置下级租户孤儿应被拒绝，得到 %v", err)
	}
	if err := authorizeOrphanMutation(ident, 7, 7); err != nil {
		t.Fatalf("部署者应能处置平台工作租户孤儿，得到 %v", err)
	}
	if err := authorizeOrphanMutation(ident, 0, 7); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("平台工作租户未接线应 fail-closed，得到 %v", err)
	}
}

// TestReconcileBackChargeWithNonReconciledStatusRejected:back_charge 仅在 status=reconciled 合法。
// cancelled/ignored 带 back_charge → 400,不扣钱(防"忽略却追扣"的矛盾输入)。
func TestReconcileBackChargeWithNonReconciledStatusRejected(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store, PlatformTenantID: 7}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/media-task-orphans/1/reconcile",
		strings.NewReader(`{"status":"ignored","back_charge":true}`))
	newRouter(d).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rr.Code)
	}
	if store.captureCalls != 0 {
		t.Fatalf("矛盾输入不应扣钱, captureCalls=%d", store.captureCalls)
	}
}
