// HUAKAI · iKun

package controlhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type tenantPolicyStoreStub struct {
	getCalls    int
	getTenantID int64
	getPolicy   registry.TenantPolicy
	getErr      error

	setCalls        int
	lastSetTenantID int64
	lastSetInherit  bool
	lastSetActor    string
	setPolicy       registry.TenantPolicy
	setErr          error
}

func (s *tenantPolicyStoreStub) GetTenantPolicy(_ context.Context, tenantID int64) (registry.TenantPolicy, error) {
	s.getCalls++
	s.getTenantID = tenantID
	if s.getErr != nil {
		return registry.TenantPolicy{}, s.getErr
	}
	return s.getPolicy, nil
}

func (s *tenantPolicyStoreStub) SetTenantInheritGlobal(_ context.Context, tenantID int64, inherit bool, actor string) (registry.TenantPolicy, error) {
	s.setCalls++
	s.lastSetTenantID = tenantID
	s.lastSetInherit = inherit
	s.lastSetActor = actor
	if s.setErr != nil {
		return registry.TenantPolicy{}, s.setErr
	}
	return s.setPolicy, nil
}

func tenantPolicyRouter(d AdminTenantPolicyDeps) chi.Router {
	r := chi.NewRouter()
	r.Method(http.MethodGet, "/v1/admin/model-registry-policy", NewAdminTenantPolicyGetHandler(d))
	r.Method(http.MethodPut, "/v1/admin/model-registry-policy", NewAdminTenantPolicySetHandler(d))
	return r
}

// withIdent 模拟 adminGate 放行后把已认证身份注入 context。
func tenantPolicyReq(method, target, body string, ident *admin.AdminIdentity) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if ident != nil {
		req = req.WithContext(admin.IdentityToContext(req.Context(), *ident))
	}
	return req
}

// 守 PUT 核心: tenant 取自 query(7), inherit 取自 body(true), actor 取自已认证身份(admin-token:4242, 非 body), 响应反映。
// mutation: handler 把 actor 取自 body/写死 / tenant 误取 body / inherit 读错 → 对应断言红。
func TestTenantPolicySet_FlipsTenantFromQueryInheritFromBodyActorFromIdentity(t *testing.T) {
	store := &tenantPolicyStoreStub{setPolicy: registry.TenantPolicy{TenantID: 7, InheritGlobalCatalog: true, UpdatedByActor: "admin-token:4242", UpdatedAt: time.Unix(1700000000, 0).UTC()}}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=7", `{"inherit_global_catalog":true}`, &admin.AdminIdentity{TokenID: 4242, Role: admin.RolePlatformAdmin}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.lastSetTenantID != 7 {
		t.Fatalf("set tenantID=%d want 7 (取自 query)", store.lastSetTenantID)
	}
	if store.lastSetInherit != true {
		t.Fatalf("set inherit=%v want true (取自 body)", store.lastSetInherit)
	}
	if store.lastSetActor != "admin-token:4242" {
		t.Fatalf("set actor=%q want admin-token:4242 (取自认证身份, 非 body)", store.lastSetActor)
	}
	var out struct {
		Policy tenantPolicyView `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Policy.TenantID != 7 || out.Policy.InheritGlobalCatalog != true {
		t.Fatalf("response policy=%+v want tenant 7 inherit true", out.Policy)
	}
}

// 守显式存在: 省略 inherit_global_catalog → 400, 不触达 store。防省略时按零值 false 静默把已继承租户改不继承。
// mutation: 去掉 req.InheritGlobalCatalog==nil 检查 → 以 false 调 store / *nil panic → 非 400/store 触达 → 红。
func TestTenantPolicySet_RequiresInheritField(t *testing.T) {
	store := &tenantPolicyStoreStub{}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=7", `{}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (inherit 必填)", rec.Code)
	}
	if store.setCalls != 0 {
		t.Fatalf("missing-inherit touched store: calls=%d", store.setCalls)
	}
}

// 守严格契约: body 未知字段 → 400(DisallowUnknownFields), 不触达 store。
// mutation: 去 DisallowUnknownFields → 未知字段静默丢 → store 触达 → 红。
func TestTenantPolicySet_RejectsUnknownFields(t *testing.T) {
	store := &tenantPolicyStoreStub{}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=7", `{"inherit_global_catalog":true,"is_superuser":true}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown field rejected)", rec.Code)
	}
	if store.setCalls != 0 {
		t.Fatalf("unknown-field touched store: calls=%d", store.setCalls)
	}
}

// 守 malformed body → 400 invalid_json(decode 错误分支), 不触达 store。区别于 RequiresInheritField({} 合法 JSON →
// invalid_tenant_policy): 此处发语法非法体 `{`, 走的是 dec.Decode 失败分支, code 必须是 invalid_json。
// mutation: decode-error 分支 code 改成别的(如复用 invalid_tenant_policy)而仍 400 → code 断言红; 漏 return 触达 store → setCalls 红。
func TestTenantPolicySet_RejectsMalformedJSON(t *testing.T) {
	store := &tenantPolicyStoreStub{}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=7", `{`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (malformed JSON)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_json") {
		t.Fatalf("body=%s want invalid_json code (decode 失败分支)", rec.Body.String())
	}
	if store.setCalls != 0 {
		t.Fatalf("malformed body touched store: calls=%d", store.setCalls)
	}
}

// 守缺 tenant_id query → 400, 不触达 store(防无作用域写)。
func TestTenantPolicySet_RequiresTenantID(t *testing.T) {
	store := &tenantPolicyStoreStub{}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy", `{"inherit_global_catalog":true}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (tenant_id 必填)", rec.Code)
	}
	if store.setCalls != 0 {
		t.Fatalf("missing tenant_id touched store: calls=%d", store.setCalls)
	}
}

// 守 store 错误映射: ErrUnknownTenant → 404(目标租户不存在, FK 违反在 store 已映射), 非 503。
// mutation: writeTenantPolicyError 的 ErrUnknownTenant 分支落 default → 503 ≠ 404 → 红。
func TestTenantPolicySet_UnknownTenant404(t *testing.T) {
	store := &tenantPolicyStoreStub{setErr: registry.ErrUnknownTenant}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=999999", `{"inherit_global_catalog":true}`, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 (unknown tenant)", rec.Code)
	}
}

// 守 GET: 读 tenant 的策略 → 响应反映 store 返回值(含默认 inherit=false)。
// mutation: GET handler 误传 tenant / 漏 inherit 映射 → 断言红。
func TestTenantPolicyGet_ReturnsPolicy(t *testing.T) {
	store := &tenantPolicyStoreStub{getPolicy: registry.TenantPolicy{TenantID: 7, InheritGlobalCatalog: true}}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodGet, "/v1/admin/model-registry-policy?tenant_id=7", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.getTenantID != 7 {
		t.Fatalf("get tenantID=%d want 7", store.getTenantID)
	}
	if !strings.Contains(rec.Body.String(), `"inherit_global_catalog":true`) {
		t.Fatalf("body=%s want inherit_global_catalog true", rec.Body.String())
	}
}

// 守 GET 缺 tenant_id → 400, 不触达 store。
func TestTenantPolicyGet_RequiresTenantID(t *testing.T) {
	store := &tenantPolicyStoreStub{}
	rec := httptest.NewRecorder()
	tenantPolicyRouter(AdminTenantPolicyDeps{Store: store}).ServeHTTP(rec,
		tenantPolicyReq(http.MethodGet, "/v1/admin/model-registry-policy", "", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if store.getCalls != 0 {
		t.Fatalf("missing tenant_id touched store: calls=%d", store.getCalls)
	}
}
