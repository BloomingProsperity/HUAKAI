package proxyadminhttp

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
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

// panicProxyQuerier 只用于证明非法 HTTP 输入会被真实 service 在数据库前拒绝。
// 若权威校验被删除，测试会触发 panic 并立即转红，而不会伪装成成功写入。
type panicProxyQuerier struct{}

func (panicProxyQuerier) CreateProxy(context.Context, admindb.CreateProxyParams) (admindb.CreateProxyRow, error) {
	panic("非法 group_id 触达 CreateProxy")
}
func (panicProxyQuerier) UpdateProxy(context.Context, admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error) {
	panic("非法 group_id 触达 UpdateProxy")
}
func (panicProxyQuerier) GetProxy(context.Context, admindb.GetProxyParams) (admindb.GetProxyRow, error) {
	panic("测试不应读取代理")
}
func (panicProxyQuerier) ListProxiesByTenant(context.Context, int64) ([]admindb.ListProxiesByTenantRow, error) {
	panic("测试不应列代理")
}
func (panicProxyQuerier) SetProxyStatus(context.Context, admindb.SetProxyStatusParams) error {
	panic("测试不应改状态")
}
func (panicProxyQuerier) SoftDeleteProxy(context.Context, admindb.SoftDeleteProxyParams) error {
	panic("测试不应删除代理")
}

// proxyServiceStub 记录每一次调用并返回预设值。它刻意构造不带任何凭据字段的
// Proxy 值(该类型本就没有),以便测试能证明:即使 create/update 输入携带了
// auth_secret,传输层也无法泄露它。
type proxyServiceStub struct {
	listCalls  int
	listTenant int64
	listRet    []proxyadmin.Proxy

	getCalls  int
	getTenant int64
	getID     int64
	getRet    proxyadmin.Proxy
	getErr    error

	createCalls int
	createIn    proxyadmin.CreateInput
	createRet   proxyadmin.Proxy
	createErr   error

	updateCalls int
	updateIn    proxyadmin.UpdateInput
	updateRet   proxyadmin.Proxy
	updateErr   error

	deleteCalls  int
	deleteTenant int64
	deleteID     int64
	deleteErr    error

	statusCalls  int
	statusTenant int64
	statusID     int64
	statusValue  string
	statusErr    error
}

func (s *proxyServiceStub) List(_ context.Context, tenantID int64) ([]proxyadmin.Proxy, error) {
	s.listCalls++
	s.listTenant = tenantID
	return s.listRet, nil
}

func (s *proxyServiceStub) Get(_ context.Context, tenantID, id int64) (proxyadmin.Proxy, error) {
	s.getCalls++
	s.getTenant, s.getID = tenantID, id
	return s.getRet, s.getErr
}

func (s *proxyServiceStub) Create(_ context.Context, in proxyadmin.CreateInput) (proxyadmin.Proxy, error) {
	s.createCalls++
	s.createIn = in
	return s.createRet, s.createErr
}

func (s *proxyServiceStub) Update(_ context.Context, in proxyadmin.UpdateInput) (proxyadmin.Proxy, error) {
	s.updateCalls++
	s.updateIn = in
	return s.updateRet, s.updateErr
}

func (s *proxyServiceStub) Delete(_ context.Context, tenantID, id int64) error {
	s.deleteCalls++
	s.deleteTenant, s.deleteID = tenantID, id
	return s.deleteErr
}

func (s *proxyServiceStub) SetStatus(_ context.Context, tenantID, id int64, status string) error {
	s.statusCalls++
	s.statusTenant, s.statusID, s.statusValue = tenantID, id, status
	return s.statusErr
}

func (s *proxyServiceStub) calls() int {
	return s.listCalls + s.getCalls + s.createCalls + s.updateCalls + s.deleteCalls + s.statusCalls
}

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admintest.TenantOperator(1, tenantID)
}

func platformAdmin() admin.AdminIdentity {
	return admintest.Platform(99)
}

func invoke(t *testing.T, d Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/proxies", func(r chi.Router) { MountRoutes(r, d) })
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

// TestListProjectsNonSecretFieldsAndScopesTenant:LIST 为解析出的租户返回不含凭据
// 的字段。变异:把错误的租户透传给 Service.List → listTenant 断言转红;
// 删掉某个被投影的字段 → body 断言转红。
func TestListProjectsNonSecretFieldsAndScopesTenant(t *testing.T) {
	user := "proxy-user"
	groupID := "group-a"
	checked := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
	svc := &proxyServiceStub{listRet: []proxyadmin.Proxy{{
		ID: 11, TenantID: 7, Name: "residential-a", Protocol: "http",
		Host: "proxy.example.com", Port: 3128, AuthUsername: &user, GroupID: &groupID, Status: "active",
		LastCheckAt: &checked, CreatedAt: checked, UpdatedAt: checked,
	}}}
	rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodGet, "/admin/v1/proxies", "")
	assertStatus(t, rec, http.StatusOK)
	if svc.listTenant != 7 {
		t.Fatalf("List tenant=%d want 7", svc.listTenant)
	}
	var body struct {
		Items []struct {
			ID           int64   `json:"id"`
			Name         string  `json:"name"`
			Protocol     string  `json:"protocol"`
			Host         string  `json:"host"`
			Port         int32   `json:"port"`
			AuthUsername *string `json:"auth_username"`
			GroupID      *string `json:"group_id"`
			Status       string  `json:"status"`
			LastCheckAt  *string `json:"last_check_at"`
		} `json:"items"`
	}
	decodeBody(t, rec, &body)
	if len(body.Items) != 1 {
		t.Fatalf("items len=%d want 1", len(body.Items))
	}
	it := body.Items[0]
	if it.ID != 11 || it.Name != "residential-a" || it.Protocol != "http" || it.Host != "proxy.example.com" ||
		it.Port != 3128 || it.Status != "active" || it.AuthUsername == nil || *it.AuthUsername != "proxy-user" {
		t.Fatalf("list projection mismatch: %+v", it)
	}
	if it.LastCheckAt == nil {
		t.Fatalf("list must project last_check_at")
	}
	if it.GroupID == nil || *it.GroupID != groupID {
		t.Fatalf("list must project group_id=%q; got %v", groupID, it.GroupID)
	}
}

// TestResponseNeverContainsAuthSecret 是泄露绊线。create 输入携带明文 auth_secret;
// 桩回显一个 Proxy(它在结构上没有凭据字段)。响应 JSON 既不能含键 "auth_secret",
// 也不能含该凭据的值。变异:给 proxyResponse 加一个 auth_secret 字段
// (或回显输入的凭据)→ 子串断言转红。
func TestResponseNeverContainsAuthSecret(t *testing.T) {
	const secret = "PLAINTEXT-PROXY-SECRET-9c1f"
	user := "u"
	now := time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, method, target, body string
		wantStatus                 int
	}{
		{
			name: "create", method: http.MethodPost, target: "/admin/v1/proxies",
			body:       `{"name":"p","protocol":"http","host":"h","port":3128,"auth_username":"u","auth_secret":"` + secret + `"}`,
			wantStatus: http.StatusCreated,
		},
		{
			name: "update", method: http.MethodPatch, target: "/admin/v1/proxies/5",
			body:       `{"name":"p","protocol":"http","host":"h","port":3128,"auth_secret":"` + secret + `"}`,
			wantStatus: http.StatusOK,
		},
		{
			name: "get", method: http.MethodGet, target: "/admin/v1/proxies/5", body: "",
			wantStatus: http.StatusOK,
		},
		{
			name: "list", method: http.MethodGet, target: "/admin/v1/proxies", body: "",
			wantStatus: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ret := proxyadmin.Proxy{ID: 5, TenantID: 7, Name: "p", Protocol: "http", Host: "h", Port: 3128,
				AuthUsername: &user, Status: "active", CreatedAt: now, UpdatedAt: now}
			svc := &proxyServiceStub{
				createRet: ret, updateRet: ret, getRet: ret,
				listRet: []proxyadmin.Proxy{ret},
			}
			rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, tc.method, tc.target, tc.body)
			assertStatus(t, rec, tc.wantStatus)
			raw := rec.Body.String()
			if strings.Contains(raw, "auth_secret") {
				t.Fatalf("%s response leaked the auth_secret key: %s", tc.name, raw)
			}
			if strings.Contains(raw, secret) {
				t.Fatalf("%s response leaked the secret VALUE: %s", tc.name, raw)
			}
		})
	}
	// 交叉核对写入路径确实收到了凭据(以证明上面的缺失是真正的脱敏,而非字段被丢弃):
	// 再跑一次全新的 create。
	svc := &proxyServiceStub{createRet: proxyadmin.Proxy{ID: 1, Name: "p", Protocol: "http", Host: "h", Port: 1, Status: "active"}}
	invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodPost, "/admin/v1/proxies",
		`{"name":"p","protocol":"http","host":"h","port":3128,"auth_secret":"`+secret+`"}`)
	if svc.createIn.AuthSecret == nil || *svc.createIn.AuthSecret != secret {
		t.Fatalf("create must pass the secret INTO the service; got %v", svc.createIn.AuthSecret)
	}
}

// TestAuthGateFiresBeforeService:未鉴权 -> 401、解析出的非管理员 -> 403,二者都
// 发生在咨询 service 之前(桩记录到零次调用)。变异:把 Resolve 调用挪到 service
// 调用之后,或删掉默认的 403 分支 → calls()!=0 或状态码错误 → 转红。
func TestAuthGateFiresBeforeService(t *testing.T) {
	cases := []struct {
		name       string
		auth       authStub
		wantStatus int
	}{
		{"unauthorized -> 401", authStub{err: admin.ErrAdminUnauthorized}, http.StatusUnauthorized},
		{"non-admin role -> 403", authStub{ident: admin.AdminIdentity{TokenID: 3, Role: "user"}}, http.StatusForbidden},
	}
	endpoints := []struct{ method, target, body string }{
		{http.MethodGet, "/admin/v1/proxies", ""},
		{http.MethodPost, "/admin/v1/proxies", `{"name":"p","protocol":"http","host":"h","port":1}`},
		{http.MethodGet, "/admin/v1/proxies/5", ""},
		{http.MethodPatch, "/admin/v1/proxies/5", `{"name":"p","protocol":"http","host":"h","port":1}`},
		{http.MethodDelete, "/admin/v1/proxies/5", ""},
		{http.MethodPut, "/admin/v1/proxies/5/status", `{"status":"disabled"}`},
	}
	for _, c := range cases {
		for _, e := range endpoints {
			svc := &proxyServiceStub{}
			rec := invoke(t, Deps{Auth: c.auth, Service: svc}, e.method, e.target, e.body)
			assertStatus(t, rec, c.wantStatus)
			if svc.calls() != 0 {
				t.Fatalf("%s %s %s touched service before auth gate: %+v", c.name, e.method, e.target, svc)
			}
		}
	}
}

// TestTenantScoping 覆盖管理门必须强制执行的三条 RBAC 路径:
//   - tenant_operator 未带 ?tenant_id 时回退到自身作用域;
//   - platform_admin 未带 ?tenant_id -> 400(必须指明租户),不触达 service;
//   - tenant_operator 指明了不同的 ?tenant_id -> 403,不触达 service。
//
// 变异:删掉 CanActOnTenant 校验 → 跨租户用例以 tenant 8 抵达 service → 转红。
func TestTenantScoping(t *testing.T) {
	t.Run("tenant_operator uses own scope", func(t *testing.T) {
		svc := &proxyServiceStub{}
		rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodGet, "/admin/v1/proxies", "")
		assertStatus(t, rec, http.StatusOK)
		if svc.listTenant != 7 {
			t.Fatalf("tenant_operator scope tenant=%d want 7", svc.listTenant)
		}
	})

	t.Run("platform_admin requires tenant_id", func(t *testing.T) {
		svc := &proxyServiceStub{}
		rec := invoke(t, Deps{Auth: authStub{ident: platformAdmin()}, Service: svc}, http.MethodGet, "/admin/v1/proxies", "")
		assertStatus(t, rec, http.StatusBadRequest)
		if svc.calls() != 0 {
			t.Fatalf("missing tenant_id must not touch service; %+v", svc)
		}
	})

	t.Run("platform_admin with tenant_id resolves it", func(t *testing.T) {
		svc := &proxyServiceStub{}
		rec := invoke(t, Deps{Auth: authStub{ident: platformAdmin()}, Service: svc}, http.MethodGet, "/admin/v1/proxies?tenant_id=3", "")
		assertStatus(t, rec, http.StatusOK)
		if svc.listTenant != 3 {
			t.Fatalf("platform_admin ?tenant_id=3 -> tenant=%d want 3", svc.listTenant)
		}
	})

	t.Run("tenant_operator cross-tenant forbidden", func(t *testing.T) {
		svc := &proxyServiceStub{}
		rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodGet, "/admin/v1/proxies?tenant_id=8", "")
		assertStatus(t, rec, http.StatusForbidden)
		if svc.calls() != 0 {
			t.Fatalf("cross-tenant must not touch service; %+v", svc)
		}
	})
}

// TestServiceErrorMapping:proxyadmin 的 sentinel 错误投影到文档化的状态码。
// 变异:把 ErrNotFound 分支并入 default → not-found 用例返回 503 → 转红。
func TestServiceErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*proxyServiceStub)
		method     string
		target     string
		body       string
		wantStatus int
	}{
		{"get not found -> 404", func(s *proxyServiceStub) { s.getErr = proxyadmin.ErrNotFound },
			http.MethodGet, "/admin/v1/proxies/5", "", http.StatusNotFound},
		{"update invalid input -> 400", func(s *proxyServiceStub) { s.updateErr = proxyadmin.ErrInvalidInput },
			http.MethodPatch, "/admin/v1/proxies/5", `{"name":"p","protocol":"http","host":"h","port":1}`, http.StatusBadRequest},
		{"set status invalid -> 400", func(s *proxyServiceStub) { s.statusErr = proxyadmin.ErrInvalidStatus },
			http.MethodPut, "/admin/v1/proxies/5/status", `{"status":"banned"}`, http.StatusBadRequest},
		{"create backend -> 503", func(s *proxyServiceStub) { s.createErr = proxyadmin.ErrBackend },
			http.MethodPost, "/admin/v1/proxies", `{"name":"p","protocol":"http","host":"h","port":1}`, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &proxyServiceStub{}
			c.setup(svc)
			rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, c.method, c.target, c.body)
			assertStatus(t, rec, c.wantStatus)
		})
	}
}

// TestSetStatusForwardsScopeAndValue:PUT /{id}/status 把 tenant、id 和 status 值
// 透传给 service。变异:硬编码某个 status,或对调 id/tenant → 断言转红。
func TestSetStatusForwardsScopeAndValue(t *testing.T) {
	svc := &proxyServiceStub{}
	rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc},
		http.MethodPut, "/admin/v1/proxies/11/status", `{"status":"dead"}`)
	assertStatus(t, rec, http.StatusOK)
	if svc.statusCalls != 1 || svc.statusTenant != 7 || svc.statusID != 11 || svc.statusValue != "dead" {
		t.Fatalf("set status forward mismatch: calls=%d tenant=%d id=%d value=%q",
			svc.statusCalls, svc.statusTenant, svc.statusID, svc.statusValue)
	}
}

// TestCreateValidationRejectsMissingFields:必填字段为空的 create 在抵达 service
// 之前就是 400。变异:删掉 handler 侧的必填字段守卫 → 空 name 请求抵达 Create →
// createCalls != 0 → 转红。
func TestCreateValidationRejectsMissingFields(t *testing.T) {
	svc := &proxyServiceStub{}
	rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc},
		http.MethodPost, "/admin/v1/proxies", `{"name":"","protocol":"http","host":"h","port":3128}`)
	assertStatus(t, rec, http.StatusBadRequest)
	if svc.createCalls != 0 {
		t.Fatalf("blank name must not reach Create; calls=%d", svc.createCalls)
	}
}

// TestDeleteReturns204:成功删除返回 204 No Content,并透传作用域。
func TestDeleteReturns204(t *testing.T) {
	svc := &proxyServiceStub{}
	rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc},
		http.MethodDelete, "/admin/v1/proxies/11", "")
	assertStatus(t, rec, http.StatusNoContent)
	if svc.deleteCalls != 1 || svc.deleteTenant != 7 || svc.deleteID != 11 {
		t.Fatalf("delete forward mismatch: calls=%d tenant=%d id=%d", svc.deleteCalls, svc.deleteTenant, svc.deleteID)
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("204 must have empty body; got %q", rec.Body.String())
	}
}

// TestCreateForwardsTenantFromScope:create 输入携带的是管理门解析出的租户,
// 而非请求体里的任何内容。变异:从请求体读取 tenant → createIn.TenantID 将不为 7 →
// 转红。
func TestCreateForwardsTenantFromScope(t *testing.T) {
	svc := &proxyServiceStub{createRet: proxyadmin.Proxy{ID: 1, Name: "p", Protocol: "http", Host: "h", Port: 1, Status: "active"}}
	rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc},
		http.MethodPost, "/admin/v1/proxies", `{"name":"p","protocol":"http","host":"h","port":3128}`)
	assertStatus(t, rec, http.StatusCreated)
	if svc.createIn.TenantID != 7 {
		t.Fatalf("create tenant=%d want 7 (from gate scope)", svc.createIn.TenantID)
	}
}

// TestProxyGroupDTOForwardsAndResponds 钉住 create/update/get 的 group_id 传输契约；
// list 路径由 TestListProjectsNonSecretFieldsAndScopesTenant 同时覆盖。变异:删请求透传、
// 响应投影或 json 键稳定性，精确值/键存在断言会转红。
func TestProxyGroupDTOForwardsAndResponds(t *testing.T) {
	groupID := "us-residential"
	base := proxyadmin.Proxy{ID: 5, TenantID: 7, Name: "p", Protocol: "http", Host: "h", Port: 3128, Status: "active"}

	t.Run("create 透传并回显组", func(t *testing.T) {
		ret := base
		ret.GroupID = &groupID
		svc := &proxyServiceStub{createRet: ret}
		rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodPost,
			"/admin/v1/proxies", `{"name":"p","protocol":"http","host":"h","port":3128,"group_id":"us-residential"}`)
		assertStatus(t, rec, http.StatusCreated)
		if svc.createIn.GroupID == nil || *svc.createIn.GroupID != groupID {
			t.Fatalf("create group_id=%v want %q", svc.createIn.GroupID, groupID)
		}
		var body map[string]any
		decodeBody(t, rec, &body)
		if body["group_id"] != groupID {
			t.Fatalf("create response group_id=%v want %q", body["group_id"], groupID)
		}
	})

	t.Run("update 显式 null 清组并稳定回显 null", func(t *testing.T) {
		svc := &proxyServiceStub{updateRet: base}
		rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodPatch,
			"/admin/v1/proxies/5", `{"name":"p","protocol":"http","host":"h","port":3128,"group_id":null}`)
		assertStatus(t, rec, http.StatusOK)
		if svc.updateIn.GroupID != nil {
			t.Fatalf("update clear group_id=%v want nil", svc.updateIn.GroupID)
		}
		var body map[string]any
		decodeBody(t, rec, &body)
		value, exists := body["group_id"]
		if !exists || value != nil {
			t.Fatalf("update response must contain group_id:null; exists=%v value=%v", exists, value)
		}
	})

	t.Run("get 回显组", func(t *testing.T) {
		ret := base
		ret.GroupID = &groupID
		svc := &proxyServiceStub{getRet: ret}
		rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodGet,
			"/admin/v1/proxies/5", "")
		assertStatus(t, rec, http.StatusOK)
		var body map[string]any
		decodeBody(t, rec, &body)
		if body["group_id"] != groupID {
			t.Fatalf("get response group_id=%v want %q", body["group_id"], groupID)
		}
	})
}

// TestProxyGroupHTTPValidationUsesRealService 通过真实 proxyadmin.Service 验证非法
// 字符与 65 字符均映射 400。变异:删除 service 校验后请求会触达 panic querier，测试转红。
func TestProxyGroupHTTPValidationUsesRealService(t *testing.T) {
	for _, groupID := range []string{"bad group!", strings.Repeat("a", 65)} {
		t.Run(groupID, func(t *testing.T) {
			svc := proxyadmin.New(panicProxyQuerier{}, nil)
			rec := invoke(t, Deps{Auth: authStub{ident: tenantOperator(7)}, Service: svc}, http.MethodPost,
				"/admin/v1/proxies", `{"name":"p","protocol":"http","host":"h","port":3128,"group_id":"`+groupID+`"}`)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}
