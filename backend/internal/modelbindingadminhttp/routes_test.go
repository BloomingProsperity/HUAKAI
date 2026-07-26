// model 绑定 admin handler 的强测试(白盒,stub,无真 DB)。覆盖 HTTP 层可判别行为:
// 双角色门、跨租户拒绝、字段校验、错误映射、默认值与 actor/tenant 传播。
// 每条都经过变异检查:把它守的那个缺陷注入,对应断言必转红(见各用例注释)。
// registry 裸 SQL + snapshot bump 的不变量走 integration_pg(需真 DB),见
// internal/registry/bindings_admin_integration_test.go。
package modelbindingadminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// ---- stubs ----

type stubAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s stubAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type stubService struct {
	// 记录入参以做传播断言
	lastListTenant int64
	lastListModel  *int64
	lastListPool   *int64
	lastCreate     registry.CreateBindingInput
	lastUpdate     registry.UpdateBindingInput
	lastDelete     registry.DeleteBindingInput
	createCalled   bool
	updateCalled   bool

	ret registry.AdminBinding
	err error
}

func TestBindingWritesAreSessionSafe(t *testing.T) {
	router := NewRouter(Deps{Auth: adminsessionauthtest.Resolver(), Service: &stubService{}})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/?tenant_id=1"},
		{http.MethodPatch, "/7?tenant_id=1"},
		{http.MethodDelete, "/7?tenant_id=1"},
	} {
		if status := adminsessionauthtest.Status(
			router, tc.method, tc.path, adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("%s %s 被管理员浏览器会话写分级门拒绝", tc.method, tc.path)
		}
	}
}

func (s *stubService) ListPoolBindingsAdmin(_ context.Context, tenantID int64, modelID, poolGroupID *int64) ([]registry.AdminBinding, error) {
	s.lastListTenant = tenantID
	s.lastListModel = modelID
	s.lastListPool = poolGroupID
	if s.err != nil {
		return nil, s.err
	}
	return []registry.AdminBinding{s.ret}, nil
}

func (s *stubService) GetPoolBindingByID(_ context.Context, _, _ int64) (registry.AdminBinding, error) {
	return s.ret, s.err
}

func (s *stubService) CreatePoolBinding(_ context.Context, in registry.CreateBindingInput) (registry.AdminBinding, error) {
	s.lastCreate = in
	s.createCalled = true
	return s.ret, s.err
}

func (s *stubService) UpdatePoolBinding(_ context.Context, in registry.UpdateBindingInput) (registry.AdminBinding, error) {
	s.lastUpdate = in
	s.updateCalled = true
	return s.ret, s.err
}

func (s *stubService) DeletePoolBinding(_ context.Context, in registry.DeleteBindingInput) error {
	s.lastDelete = in
	return s.err
}

func platformAdmin(tokenID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: tokenID, Role: admin.RolePlatformAdmin}
}
func tenantOperator(tokenID, scope int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: tokenID, Role: admin.RoleTenantOperator, ScopeTenantID: scope}
}

func do(t *testing.T, auth stubAuth, svc *stubService, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := chi.NewRouter()
	h.Use(middleware.RequestID)
	MountRoutes(h, Deps{Auth: auth, Service: svc})
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// platform_admin 不带 ?tenant_id → 400 且 service 不被调用。
// 变异:删掉 tenantFromQueryOrScope 里的 tenant_id_required 分支 → 会放行 → 红。
func TestPlatformAdminRequiresTenantID(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodGet, "/", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
	if svc.lastListTenant != 0 {
		t.Fatalf("service 被调用(tenant=%d),门应在此之前短路", svc.lastListTenant)
	}
}

// tenant_operator 自动域到自己租户:service 收到 scope 租户。
// 变异:把 tenantFromQueryOrScope 改成不回 ScopeTenantID → 红。
func TestTenantOperatorScopedToOwnTenant(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: tenantOperator(7, 42)}, svc, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", rec.Code)
	}
	if svc.lastListTenant != 42 {
		t.Fatalf("service tenant=%d want 42(自 scope)", svc.lastListTenant)
	}
}

// 跨租户拒绝:operator(scope=42)带 ?tenant_id=99 → 403,service 不被调用。
// 变异:门跳过 CanIssueForTenant → 会放行到 99 → service 被调用 → 红。
func TestCrossTenantForbidden(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: tenantOperator(7, 42)}, svc, http.MethodGet, "/?tenant_id=99", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
	if svc.lastListTenant != 0 {
		t.Fatalf("service 被调用(tenant=%d),跨租户应被门挡", svc.lastListTenant)
	}
}

// 鉴权失败 → 401。
func TestUnauthorized(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{err: admin.ErrAdminUnauthorized}, svc, http.MethodGet, "/?tenant_id=1", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

// 创建最小载荷 → 默认值与 actor/tenant 正确传到 service。
// 变异:改任一默认(如 Priority 不再默认 100)或不传 Actor → 红。
func TestCreateDefaultsAndActorPropagation(t *testing.T) {
	svc := &stubService{}
	body := `{"model_id":5,"pool_group_id":9}`
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=42", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d want 201; body=%s", rec.Code, rec.Body.String())
	}
	c := svc.lastCreate
	if c.TenantID != 42 || c.ModelID != 5 || c.PoolGroupID != 9 {
		t.Fatalf("ids 错:tenant=%d model=%d pool=%d", c.TenantID, c.ModelID, c.PoolGroupID)
	}
	if c.Priority != 100 || c.Weight != 1 || c.SelectionMode != "strict_priority" || c.FallbackClass != "normal" || !c.Enabled {
		t.Fatalf("默认值错:pri=%d w=%d sel=%q fb=%q en=%v", c.Priority, c.Weight, c.SelectionMode, c.FallbackClass, c.Enabled)
	}
	if c.Actor != "admin_token:7" || c.ActorRole != admin.RolePlatformAdmin || strings.TrimSpace(c.RequestID) == "" {
		t.Fatalf("操作日志身份传播错误 actor=%q role=%q request_id=%q", c.Actor, c.ActorRole, c.RequestID)
	}
}

// 老客户端仍可携带三个仅存储字段；删掉任一 DTO 字段后严格 JSON 解码会返回 400，测试随即转红。
func TestCreateAcceptsLegacyStoredOnlyFields(t *testing.T) {
	svc := &stubService{}
	body := `{"model_id":5,"pool_group_id":9,"weight":7,"max_parallel_requests":3,"fallback_class":"quota","selection_mode":"priority_weighted","enabled":false}`
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=42", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d want 201; body=%s", rec.Code, rec.Body.String())
	}
	c := svc.lastCreate
	if !svc.createCalled || c.Weight != 7 || c.MaxParallelRequests == nil || *c.MaxParallelRequests != 3 || c.FallbackClass != "quota" {
		t.Fatalf("旧字段未兼容透传:called=%v input=%+v", svc.createCalled, c)
	}
	if c.SelectionMode != "priority_weighted" || c.Enabled {
		t.Fatalf("现存有效字段传播错:selection_mode=%q enabled=%v", c.SelectionMode, c.Enabled)
	}
}

// 缺 model_id/pool_group_id → 400。
func TestCreateRequiresModelAndPool(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=1", `{"pool_group_id":9}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
	if svc.createCalled {
		t.Fatalf("校验失败仍调用了 service")
	}
}

// 非法 selection_mode → 400,service 不被调用。
// 变异:删 validateCommon 的 selection_mode 检查 → service 被调用(201)→ 红。
func TestCreateRejectsBadSelectionMode(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"selection_mode":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
	if svc.createCalled {
		t.Fatalf("非法 selection_mode 仍调用了 service")
	}
}

// weight<=0 → 400。
func TestCreateRejectsNonPositiveWeight(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"weight":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400 (weight 0)", rec.Code)
	}
}

// 负并发上限必须在进入 service 前返回 400；0 明确定义为不限，仍可写入。
func TestCreateMaxParallelRequestsValidation(t *testing.T) {
	rejected := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, rejected, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"max_parallel_requests":-1}`)
	if rec.Code != http.StatusBadRequest || rejected.createCalled {
		t.Fatalf("负上限 code=%d createCalled=%v want 400/false", rec.Code, rejected.createCalled)
	}

	accepted := &stubService{}
	rec = do(t, stubAuth{ident: platformAdmin(7)}, accepted, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"max_parallel_requests":0}`)
	if rec.Code != http.StatusCreated || !accepted.createCalled || accepted.lastCreate.MaxParallelRequests == nil || *accepted.lastCreate.MaxParallelRequests != 0 {
		t.Fatalf("零上限 code=%d input=%+v want 201 且透传 0", rec.Code, accepted.lastCreate)
	}
}

// 负 RPM/TPM 必须在进入 service 前稳定返回 400，不能依赖数据库 CHECK 再变成 503。
func TestCreateRejectsNegativeRateLimits(t *testing.T) {
	for _, body := range []string{
		`{"model_id":5,"pool_group_id":9,"rpm_limit":-1}`,
		`{"model_id":5,"pool_group_id":9,"tpm_limit":-1}`,
	} {
		svc := &stubService{}
		rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=1", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s code=%d want 400", body, rec.Code)
		}
		if svc.createCalled {
			t.Errorf("body=%s 校验失败仍调用 create", body)
		}
	}
}

// 生效窗自证:from>=until 拒;from<until 收。两向都断,确保不是恒红/恒绿。
// 变异:删 ef.Before(eu) 检查 → 倒序也放行(201)→ 第一半红。
func TestCreateEffectiveWindowDiscriminates(t *testing.T) {
	// 倒序 → 400
	svc1 := &stubService{}
	rec1 := do(t, stubAuth{ident: platformAdmin(7)}, svc1, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"effective_from":"2026-02-02T00:00:00Z","effective_until":"2026-01-01T00:00:00Z"}`)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("倒序窗 code=%d want 400", rec1.Code)
	}
	if svc1.createCalled {
		t.Fatalf("倒序窗仍调用了 service")
	}
	// 正序 → 201(正控制,证明不是恒拒)
	svc2 := &stubService{}
	rec2 := do(t, stubAuth{ident: platformAdmin(7)}, svc2, http.MethodPost, "/?tenant_id=1",
		`{"model_id":5,"pool_group_id":9,"effective_from":"2026-01-01T00:00:00Z","effective_until":"2026-02-02T00:00:00Z"}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("正序窗 code=%d want 201; body=%s", rec2.Code, rec2.Body.String())
	}
}

// PATCH 只传播请求中出现的字段。变异:恢复旧默认填充后，Priority/SelectionMode
// 等字段的 Set 会错误变为 true，本测试转红。
func TestUpdatePreservesOmittedFields(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPatch, "/5?tenant_id=42", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := svc.lastUpdate
	if !svc.updateCalled || !got.Enabled.Set || got.Enabled.Value {
		t.Fatalf("enabled PATCH 未传播: called=%v input=%+v", svc.updateCalled, got)
	}
	if got.Priority.Set || got.Weight.Set || got.SelectionMode.Set || got.FallbackClass.Set ||
		got.ProviderModelIDOverride.Set || got.RPMLimit.Set || got.TPMLimit.Set ||
		got.MaxParallelRequests.Set || got.DisabledReason.Set || got.EffectiveFrom.Set ||
		got.EffectiveUntil.Set || got.Reason.Set {
		t.Fatalf("省略字段被错误标成更新: %+v", got)
	}
	if got.Actor != "admin_token:7" || got.ActorRole != admin.RolePlatformAdmin || strings.TrimSpace(got.RequestID) == "" {
		t.Fatalf("更新日志身份传播错误: %+v", got)
	}
}

// 可空字段的显式 null 是清空，不是省略。
func TestUpdateExplicitNullClearsNullableFields(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPatch, "/5?tenant_id=42",
		`{"provider_model_id_override":null,"rpm_limit":null,"effective_until":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := svc.lastUpdate
	if !got.ProviderModelIDOverride.Set || got.ProviderModelIDOverride.Value != nil ||
		!got.RPMLimit.Set || got.RPMLimit.Value != nil ||
		!got.EffectiveUntil.Set || got.EffectiveUntil.Value != nil {
		t.Fatalf("显式 null 未按清空传播: %+v", got)
	}
}

func TestUpdateRejectsNullForRequiredFieldAndEmptyPatch(t *testing.T) {
	for _, body := range []string{`{"enabled":null}`, `{}`} {
		svc := &stubService{}
		rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPatch, "/5?tenant_id=42", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s code=%d want 400", body, rec.Code)
		}
		if svc.updateCalled {
			t.Errorf("body=%s 校验失败仍调用 update", body)
		}
	}
}

// registry 哨兵 → HTTP 状态的映射,逐个判别(每个码不同)。
// 变异:任一 case 映射改错 → 对应行红。
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{registry.ErrBindingConflict, http.StatusConflict},
		{registry.ErrModelNotBindable, http.StatusUnprocessableEntity},
		{registry.ErrPoolGroupNotFound, http.StatusUnprocessableEntity},
		{registry.ErrBindingNotFound, http.StatusNotFound},
		{registry.ErrBindingInvalid, http.StatusBadRequest},
	}
	for _, c := range cases {
		svc := &stubService{err: c.err}
		rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodPost, "/?tenant_id=1",
			`{"model_id":5,"pool_group_id":9}`)
		if rec.Code != c.want {
			t.Errorf("err=%v code=%d want %d", c.err, rec.Code, c.want)
		}
	}
}

// DELETE 把 id + tenant + actor 正确传给 service。
// 变异:newDeleteHandler 不传 actor / 传错 tenant → 红。
func TestDeletePropagatesIDTenantActor(t *testing.T) {
	svc := &stubService{}
	rec := do(t, stubAuth{ident: tenantOperator(7, 42)}, svc, http.MethodDelete, "/5", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}
	if svc.lastDelete.ID != 5 || svc.lastDelete.TenantID != 42 || svc.lastDelete.Actor != "admin_token:7" ||
		svc.lastDelete.ActorRole != admin.RoleTenantOperator || strings.TrimSpace(svc.lastDelete.RequestID) == "" {
		t.Fatalf("删除日志身份传播错误:%+v", svc.lastDelete)
	}
}

// 健全性:list 成功返回 items 信封 + 200。
func TestListEnvelope(t *testing.T) {
	svc := &stubService{ret: registry.AdminBinding{ID: 1, ModelID: 5, PoolGroupID: 9, Priority: 100, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "normal", Enabled: true}}
	rec := do(t, stubAuth{ident: platformAdmin(7)}, svc, http.MethodGet, "/?tenant_id=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", rec.Code)
	}
	var env struct {
		Items []bindingResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析 envelope: %v", err)
	}
	if len(env.Items) != 1 || env.Items[0].ID != 1 {
		t.Fatalf("items 错: %+v", env.Items)
	}
}
