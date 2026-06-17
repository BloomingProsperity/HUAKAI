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
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
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
	lastDeleteID   int64
	lastDeleteTen  int64
	lastDeleteActr string
	createCalled   bool

	ret registry.AdminBinding
	err error
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
	return s.ret, s.err
}

func (s *stubService) DeletePoolBinding(_ context.Context, id, tenantID int64, actor, _ string) error {
	s.lastDeleteID = id
	s.lastDeleteTen = tenantID
	s.lastDeleteActr = actor
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
	h := NewRouter(Deps{Auth: auth, Service: svc})
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
	if c.Actor != "admin-token:7" {
		t.Fatalf("actor=%q want admin-token:7", c.Actor)
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
	if svc.lastDeleteID != 5 || svc.lastDeleteTen != 42 || svc.lastDeleteActr != "admin-token:7" {
		t.Fatalf("传播错:id=%d tenant=%d actor=%q", svc.lastDeleteID, svc.lastDeleteTen, svc.lastDeleteActr)
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
