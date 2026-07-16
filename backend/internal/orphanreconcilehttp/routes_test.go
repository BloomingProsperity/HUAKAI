package orphanreconcilehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
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
	d := Deps{Auth: fakeAuth{ident: admintest.Platform(0)}, Store: store}
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
	d := Deps{Auth: fakeAuth{ident: admintest.TenantOperator(0, 9)}, Store: store}
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
	d := Deps{Auth: fakeAuth{ident: admintest.Platform(0)}, Store: store}
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
	d := Deps{Auth: fakeAuth{ident: admintest.Platform(0)}, Store: store}
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
// 租户的孤儿,audit hook 在触碰任何 DB 前就返回 errOrphanForbiddenTenant(回滚整笔)。
// 判别:若去掉 CanActOnTenant 越权检查 → hook 会继续往下写 admin_audit_events(对 nil tx
// panic 或越权写库),本测试断言它返回 errOrphanForbiddenTenant RED。
func TestBuildAuditHookForbidsCrossTenant(t *testing.T) {
	ident := admintest.TenantOperator(0, 7)
	hook := buildAuditHook(ident, reconcileRequest{Status: "reconciled"}, "req-x")
	// 孤儿属租户 9,operator 限租户 7 → 越权,必须在写库前拒绝。
	err := hook(context.Background(), nil /*tx 不会被触碰*/, mediatask.OrphanReconcileResult{
		OrphanID: 2, TaskID: 102, TenantID: 9, UserID: 43, Status: "reconciled",
	})
	if err == nil {
		t.Fatalf("跨租户对账应被越权守卫拒绝, got nil")
	}
	if err.Error() != errOrphanForbiddenTenant.Error() {
		t.Fatalf("越权应返回 errOrphanForbiddenTenant, got %v", err)
	}
}

// TestReconcileBackChargeWithNonReconciledStatusRejected:back_charge 仅在 status=reconciled 合法。
// cancelled/ignored 带 back_charge → 400,不扣钱(防"忽略却追扣"的矛盾输入)。
func TestReconcileBackChargeWithNonReconciledStatusRejected(t *testing.T) {
	store := newFakeStore(sampleOrphans()...)
	d := Deps{Auth: fakeAuth{ident: admintest.Platform(0)}, Store: store}
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
