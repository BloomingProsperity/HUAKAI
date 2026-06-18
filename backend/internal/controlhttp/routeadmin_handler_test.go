// HUAKAI · iKun

package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/routeadmin"
)

type routeAdminStubAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s routeAdminStubAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type routeAdminStubService struct {
	called     bool
	lastCreate routeadmin.CreateInput
	lastUpdate routeadmin.UpdateInput
	lastDelAdm int64
	createFn   func(routeadmin.CreateInput) (routeadmin.Route, error)
	listFn     func(int64) ([]routeadmin.Route, error)
	getFn      func(int64, int64) (routeadmin.Route, error)
	updateFn   func(routeadmin.UpdateInput) (routeadmin.Route, error)
	deleteFn   func(int64, int64, int64) (routeadmin.Route, error)
}

func (s *routeAdminStubService) Create(_ context.Context, in routeadmin.CreateInput) (routeadmin.Route, error) {
	s.called = true
	s.lastCreate = in
	if s.createFn != nil {
		return s.createFn(in)
	}
	return routeadmin.Route{ID: 1, TenantID: in.TenantID, Name: in.Name, PoolGroupID: in.PoolGroupID, MatchPriority: 100, Enabled: true}, nil
}
func (s *routeAdminStubService) List(_ context.Context, tenantID int64) ([]routeadmin.Route, error) {
	s.called = true
	if s.listFn != nil {
		return s.listFn(tenantID)
	}
	return nil, nil
}
func (s *routeAdminStubService) Get(_ context.Context, tenantID, id int64) (routeadmin.Route, error) {
	s.called = true
	if s.getFn != nil {
		return s.getFn(tenantID, id)
	}
	return routeadmin.Route{ID: id, TenantID: tenantID}, nil
}
func (s *routeAdminStubService) Update(_ context.Context, in routeadmin.UpdateInput) (routeadmin.Route, error) {
	s.called = true
	s.lastUpdate = in
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	// 默认回完整 route(全字段回填), 让 handler 测试能断言响应体序列化, 不只看输入透传。
	mp := 100
	if in.MatchPriority != nil {
		mp = *in.MatchPriority
	}
	return routeadmin.Route{
		ID: in.ID, TenantID: in.TenantID, Name: in.Name, UserGroupMatch: in.UserGroupMatch,
		ModelPatternMatch: in.ModelPatternMatch, PoolGroupID: in.PoolGroupID, MatchPriority: mp, Enabled: true,
	}, nil
}
func (s *routeAdminStubService) Delete(_ context.Context, tenantID, id, adminID int64) (routeadmin.Route, error) {
	s.called = true
	s.lastDelAdm = adminID
	if s.deleteFn != nil {
		return s.deleteFn(tenantID, id, adminID)
	}
	return routeadmin.Route{ID: id, TenantID: tenantID}, nil
}

func routeAdminPlatformAdmin(tokenID int64) routeAdminStubAuth {
	return routeAdminStubAuth{ident: admin.AdminIdentity{TokenID: tokenID, Role: admin.RolePlatformAdmin}}
}

func newRouteAdminTestServer(d RouteAdminDeps) *httptest.Server {
	r := chi.NewRouter()
	r.Route("/v1/admin/routes", func(r chi.Router) {
		MountRouteAdminRoutes(r, d)
	})
	return httptest.NewServer(r)
}

func doRouteAdminReq(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// 守审计/授权核心: 创建时传给 service 的 AdminID 必取自已认证身份 (TokenID=4242), 绝不来自请求体。
// mutation: handler 把 AdminID 写成 0 / 体内字段 → captured.AdminID != 4242 → 红。
func TestCreate_UsesAuthenticatedAdminIDNotBody(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(4242), Service: svc})
	defer ts.Close()

	body := `{"tenant_id":5,"name":"premium-claude","user_group_match":"premium","model_pattern_match":"claude-*","pool_group_id":9}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPost, "/v1/admin/routes/", body)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d, want 201", status)
	}
	if svc.lastCreate.AdminID != 4242 {
		t.Fatalf("AdminID passed to service = %d, want 4242 (from authenticated identity, not body)", svc.lastCreate.AdminID)
	}
	if svc.lastCreate.TenantID != 5 || svc.lastCreate.Name != "premium-claude" || svc.lastCreate.PoolGroupID != 9 {
		t.Fatalf("create input not faithfully forwarded: %+v", svc.lastCreate)
	}
}

// 守严格请求体 / 不可伪造: 请求体携带未知字段 (如试图注入 admin_id 覆盖审计归属) → 400, service 不触达。
// 与上一测互补: 上测守 AdminID 取自身份, 本测守 DisallowUnknownFields 真生效 (注入字段被拒而非静默忽略)。
// mutation: routeAdminDecodeJSON 去掉 DisallowUnknownFields() → admin_id 被静默忽略 → 201 + svc.called → 红。
func TestCreate_RejectsUnknownFields(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(4242), Service: svc})
	defer ts.Close()

	body := `{"tenant_id":5,"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9,"admin_id":999}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPost, "/v1/admin/routes/", body)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown field (admin_id injection) status=%d, want 400", status)
	}
	if svc.called {
		t.Fatal("service must NOT run when request body carries unknown fields (strict decode blocks admin_id injection)")
	}
}

// 守越权: 非 platform_admin (如 tenant_operator) 必 403, 且绝不触达 service。
// mutation: routeAdminResolveAdmin 删掉 role==RolePlatformAdmin 检查 → 201 + svc.called=true → 红。
func TestCreate_NonPlatformAdminForbidden(t *testing.T) {
	svc := &routeAdminStubService{}
	auth := routeAdminStubAuth{ident: admin.AdminIdentity{TokenID: 9, Role: admin.RoleTenantOperator}}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: auth, Service: svc})
	defer ts.Close()

	body := `{"tenant_id":5,"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPost, "/v1/admin/routes/", body)
	if status != http.StatusForbidden {
		t.Fatalf("non-admin create status=%d, want 403", status)
	}
	if svc.called {
		t.Fatal("service must NOT be invoked when role check fails (privilege escalation guard)")
	}
}

// 守认证失败映射: 通用解析错→401; 后端瞬态 (ErrAdminBackend)→503。
func TestAuth_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"generic unauthorized", errors.New("bad token"), http.StatusUnauthorized},
		{"backend transient", admin.ErrAdminBackend, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		svc := &routeAdminStubService{}
		ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminStubAuth{err: c.err}, Service: svc})
		status, _ := doRouteAdminReq(t, ts, http.MethodGet, "/v1/admin/routes/?tenant_id=5", "")
		ts.Close()
		if status != c.want {
			t.Fatalf("%s: status=%d, want %d", c.name, status, c.want)
		}
		if svc.called {
			t.Fatalf("%s: service must not run on auth failure", c.name)
		}
	}
}

// 守服务层错误→HTTP 状态码映射 (错码会误导调用方; 重名 409 / 不存在 404 / 非法 400 / 后端 503 各不同)。
// mutation: routeAdminWriteRouteError 任一分支落到 default → 该 case 变红。
func TestServiceError_StatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"duplicate name", routeadmin.ErrDuplicateName, http.StatusConflict},
		{"invalid model pattern", routeadmin.ErrInvalidModelPattern, http.StatusBadRequest},
		{"invalid input", routeadmin.ErrInvalidInput, http.StatusBadRequest},
		{"pool group not found", routeadmin.ErrPoolGroupNotFound, http.StatusNotFound},
		{"unknown backend error", errors.New("pg down"), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		svc := &routeAdminStubService{createFn: func(routeadmin.CreateInput) (routeadmin.Route, error) { return routeadmin.Route{}, c.err }}
		ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(1), Service: svc})
		body := `{"tenant_id":5,"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9}`
		status, _ := doRouteAdminReq(t, ts, http.MethodPost, "/v1/admin/routes/", body)
		ts.Close()
		if status != c.want {
			t.Fatalf("%s: status=%d, want %d", c.name, status, c.want)
		}
	}
}

// 守 Get 的 ErrRouteNotFound→404。
func TestGet_NotFound(t *testing.T) {
	svc := &routeAdminStubService{getFn: func(int64, int64) (routeadmin.Route, error) { return routeadmin.Route{}, routeadmin.ErrRouteNotFound }}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(1), Service: svc})
	defer ts.Close()
	status, _ := doRouteAdminReq(t, ts, http.MethodGet, "/v1/admin/routes/123?tenant_id=5", "")
	if status != http.StatusNotFound {
		t.Fatalf("get missing route status=%d, want 404", status)
	}
}

// 守软删归属: DELETE 传给 service 的 adminID 取自已认证身份 (TokenID=7), 非请求体/零值。
// mutation: handler 传 0 → lastDelAdm != 7 → 红。
func TestDelete_UsesAuthenticatedAdminID(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(7), Service: svc})
	defer ts.Close()
	status, _ := doRouteAdminReq(t, ts, http.MethodDelete, "/v1/admin/routes/55?tenant_id=5", "")
	if status != http.StatusOK {
		t.Fatalf("delete status=%d, want 200", status)
	}
	if svc.lastDelAdm != 7 {
		t.Fatalf("delete adminID passed to service = %d, want 7 (authenticated identity)", svc.lastDelAdm)
	}
}

// 守 PUT 编辑核心: AdminID 取自已认证身份(4242)、tenant 取自 query(5)、id 取自 path(55), body 字段如实透传。
// mutation: handler 把 AdminID 写 0、或 tenant 误取 body → lastUpdate 对应字段不符 → 红。
func TestUpdate_UsesAuthenticatedAdminIDAndTenantFromQuery(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(4242), Service: svc})
	defer ts.Close()

	body := `{"name":"premium-claude","user_group_match":"premium","model_pattern_match":"claude-*","pool_group_id":9,"match_priority":5}`
	status, out := doRouteAdminReq(t, ts, http.MethodPut, "/v1/admin/routes/55?tenant_id=5", body)
	if status != http.StatusOK {
		t.Fatalf("update status=%d, want 200", status)
	}
	if svc.lastUpdate.AdminID != 4242 {
		t.Fatalf("AdminID passed to service = %d, want 4242 (authenticated identity)", svc.lastUpdate.AdminID)
	}
	if svc.lastUpdate.TenantID != 5 || svc.lastUpdate.ID != 55 {
		t.Fatalf("tenant/id not from query/path: tenant=%d id=%d, want 5/55", svc.lastUpdate.TenantID, svc.lastUpdate.ID)
	}
	if svc.lastUpdate.Name != "premium-claude" || svc.lastUpdate.PoolGroupID != 9 ||
		svc.lastUpdate.MatchPriority == nil || *svc.lastUpdate.MatchPriority != 5 {
		t.Fatalf("update input not faithfully forwarded: %+v", svc.lastUpdate)
	}
	// 守响应体序列化(端到端): 返回的 route 须含更新后字段。
	// mutation: routeAdminToRouteView 漏映射某列(如 pool_group_id)或 handler 回旧值 → 该断言红(stub 已回完整 route)。
	route, ok := out["route"].(map[string]any)
	if !ok {
		t.Fatalf("response missing route object: %+v", out)
	}
	if route["id"] != float64(55) || route["tenant_id"] != float64(5) || route["name"] != "premium-claude" ||
		route["user_group_match"] != "premium" || route["model_pattern_match"] != "claude-*" ||
		route["pool_group_id"] != float64(9) || route["match_priority"] != float64(5) || route["enabled"] != true {
		t.Fatalf("response route not serialized with updated fields: %+v", route)
	}
}

// 守不可经更新走私租户: body 携带 tenant_id (试图跨租户搬移) → 400(DisallowUnknownFields 拒), service 不触达。
// 这是 PUT 的关键安全属性: 行只能由 path id + query tenant 定位, body 无 tenant_id 字段, 故注入即拒。
// mutation: updateRouteRequest 加上 tenant_id 字段(或解码去 DisallowUnknownFields) → 走私被静默接受 → 200/svc.called → 红。
func TestUpdate_RejectsUnknownFieldsBlocksTenantSmuggling(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(4242), Service: svc})
	defer ts.Close()

	body := `{"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9,"tenant_id":6}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPut, "/v1/admin/routes/55?tenant_id=5", body)
	if status != http.StatusBadRequest {
		t.Fatalf("tenant_id in body status=%d, want 400 (smuggling blocked)", status)
	}
	if svc.called {
		t.Fatal("service must NOT run when body carries tenant_id (cross-tenant move smuggling)")
	}
}

// 守 PUT 缺 tenant_id query → 400, 不触达 service。
// mutation: routeAdminParsePositiveQuery 放行空 → service 以 tenant=0 调用 → 非 400 → 红。
func TestUpdate_RequiresTenantID(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(1), Service: svc})
	defer ts.Close()
	body := `{"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPut, "/v1/admin/routes/55", body)
	if status != http.StatusBadRequest {
		t.Fatalf("update without tenant_id status=%d, want 400", status)
	}
	if svc.called {
		t.Fatal("service must not run when tenant_id is missing")
	}
}

// 守越权: 非 platform_admin PUT → 403, 绝不触达 service。
// mutation: routeAdminResolveAdmin 删 role 检查 → 200 + svc.called → 红。
func TestUpdate_NonPlatformAdminForbidden(t *testing.T) {
	svc := &routeAdminStubService{}
	auth := routeAdminStubAuth{ident: admin.AdminIdentity{TokenID: 9, Role: admin.RoleTenantOperator}}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: auth, Service: svc})
	defer ts.Close()
	body := `{"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9}`
	status, _ := doRouteAdminReq(t, ts, http.MethodPut, "/v1/admin/routes/55?tenant_id=5", body)
	if status != http.StatusForbidden {
		t.Fatalf("non-admin update status=%d, want 403", status)
	}
	if svc.called {
		t.Fatal("service must NOT be invoked when role check fails")
	}
}

// 守 Update 的服务层错误映射: ErrRouteNotFound→404, ErrPoolGroupNotFound→404, ErrDuplicateName→409, ErrInvalidModelPattern→400。
// mutation: routeAdminWriteRouteError 对应分支落 default → 该 case 非期望码 → 红。
func TestUpdate_ServiceErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"route not found", routeadmin.ErrRouteNotFound, http.StatusNotFound},
		{"pool group not found", routeadmin.ErrPoolGroupNotFound, http.StatusNotFound},
		{"duplicate name", routeadmin.ErrDuplicateName, http.StatusConflict},
		{"invalid model pattern", routeadmin.ErrInvalidModelPattern, http.StatusBadRequest},
	}
	for _, c := range cases {
		svc := &routeAdminStubService{updateFn: func(routeadmin.UpdateInput) (routeadmin.Route, error) { return routeadmin.Route{}, c.err }}
		ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(1), Service: svc})
		body := `{"name":"r1","user_group_match":"premium","model_pattern_match":"*","pool_group_id":9}`
		status, _ := doRouteAdminReq(t, ts, http.MethodPut, "/v1/admin/routes/55?tenant_id=5", body)
		ts.Close()
		if status != c.want {
			t.Fatalf("%s: status=%d, want %d", c.name, status, c.want)
		}
	}
}

// 守 tenant_id 必填: List/Get 缺 tenant_id → 400, 不触达 service (防全租户泄列)。
// mutation: routeAdminParsePositiveQuery 放行空值 → service 以 0 调用 → 状态非 400 → 红。
func TestList_RequiresTenantID(t *testing.T) {
	svc := &routeAdminStubService{}
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: routeAdminPlatformAdmin(1), Service: svc})
	defer ts.Close()
	status, _ := doRouteAdminReq(t, ts, http.MethodGet, "/v1/admin/routes/", "")
	if status != http.StatusBadRequest {
		t.Fatalf("list without tenant_id status=%d, want 400", status)
	}
	if svc.called {
		t.Fatal("service must not run when tenant_id is missing (avoid unscoped listing)")
	}
}

// 守依赖未配置: RouteAdminService/Auth 为 nil → 503, 不 panic。
func TestNilDeps_ServiceUnavailable(t *testing.T) {
	ts := newRouteAdminTestServer(RouteAdminDeps{Auth: nil, Service: nil})
	defer ts.Close()
	status, _ := doRouteAdminReq(t, ts, http.MethodGet, "/v1/admin/routes/?tenant_id=5", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("nil deps status=%d, want 503", status)
	}
}
