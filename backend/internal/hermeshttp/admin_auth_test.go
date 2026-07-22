package hermeshttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesprincipal"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

type fakeAdminResolver struct {
	identity admin.AdminIdentity
	err      error
	called   bool
}

func (f *fakeAdminResolver) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	f.called = true
	return f.identity, f.err
}

type fakeCapabilityChecker struct {
	allowed       bool
	err           error
	called        bool
	tenantID      int64
	capabilityKey string
}

func (f *fakeCapabilityChecker) Allowed(_ context.Context, tenantID int64, capability string) (bool, error) {
	f.called = true
	f.tenantID = tenantID
	f.capabilityKey = capability
	return f.allowed, f.err
}

type fakePrincipalEnsurer struct {
	principal hermesprincipal.Principal
	err       error
	called    bool
	tenantID  int64
}

func (f *fakePrincipalEnsurer) Ensure(_ context.Context, tenantID int64) (hermesprincipal.Principal, error) {
	f.called = true
	f.tenantID = tenantID
	return f.principal, f.err
}

type captureNext struct {
	gotIdentity sessionauth.Identity
	gotActor    adminActor
	gotActorOK  bool
	served      bool
}

func (c *captureNext) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.served = true
	if id, ok := r.Context().Value(authContextKey{}).(sessionauth.Identity); ok {
		c.gotIdentity = id
	}
	c.gotActor, c.gotActorOK = adminActorFromContext(r.Context())
}

func runAdminMiddleware(deps AdminAuthDeps, path string) (*httptest.ResponseRecorder, *captureNext) {
	next := &captureNext{}
	h := AdminAuthMiddleware(deps)(next)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, next
}

func completeAdminDeps(resolver AdminAuthResolver) (AdminAuthDeps, *fakeCapabilityChecker, *fakePrincipalEnsurer) {
	capabilities := &fakeCapabilityChecker{allowed: true}
	principals := &fakePrincipalEnsurer{principal: hermesprincipal.Principal{TenantID: 1, UserID: 81, APIKeyID: 91}}
	return AdminAuthDeps{
		Resolver:         resolver,
		PlatformTenantID: 1,
		Capabilities:     capabilities,
		Principals:       principals,
	}, capabilities, principals
}

func TestAdminAuthMiddleware认证失败时拒绝请求(t *testing.T) {
	deps, _, _ := completeAdminDeps(&fakeAdminResolver{err: admin.ErrAdminUnauthorized})
	rec, next := runAdminMiddleware(deps, "/conversations")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d，响应=%s，期望 401", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatal("认证失败后仍进入了业务处理器")
	}
}

func TestAdminAuthMiddleware认证后端异常时返回503(t *testing.T) {
	deps, _, _ := completeAdminDeps(&fakeAdminResolver{err: admin.ErrAdminBackend})
	rec, next := runAdminMiddleware(deps, "/conversations")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d，响应=%s，期望 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatal("认证后端异常后仍进入了业务处理器")
	}
}

func TestAdminAuthMiddleware依赖不完整时关闭入口(t *testing.T) {
	rec, next := runAdminMiddleware(AdminAuthDeps{}, "/conversations")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d，响应=%s，期望 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatal("依赖不完整时仍进入了业务处理器")
	}
}

func TestAdminAuthMiddleware拒绝请求覆盖身份范围(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 7,
		Source:  admin.AdminSourceToken,
		Role:    admin.RolePlatformAdmin,
	}}
	deps, _, principals := completeAdminDeps(resolver)
	for _, path := range []string{
		"/conversations?tenant_id=9",
		"/conversations?as_user_id=42",
	} {
		rec, next := runAdminMiddleware(deps, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("路径=%s，状态码=%d，响应=%s，期望 400", path, rec.Code, rec.Body.String())
		}
		if next.served || resolver.called || principals.called {
			t.Fatalf("路径=%s 的旧身份覆盖参数未在认证前被拒绝", path)
		}
	}
}

func TestAdminAuthMiddleware部署者固定使用平台租户(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 41,
		Source:  admin.AdminSourceToken,
		Role:    admin.RolePlatformAdmin,
	}}
	deps, capabilities, principals := completeAdminDeps(resolver)
	rec, next := runAdminMiddleware(deps, "/conversations")
	if rec.Code != http.StatusOK || !next.served {
		t.Fatalf("状态码=%d，响应=%s，处理器到达=%v", rec.Code, rec.Body.String(), next.served)
	}
	if capabilities.called {
		t.Fatal("部署者不应查询下级租户能力授权")
	}
	if !principals.called || principals.tenantID != 1 {
		t.Fatalf("服务主体租户=%d，期望平台租户 1", principals.tenantID)
	}
	if next.gotIdentity != (sessionauth.Identity{TenantID: 1, UserID: 81, APIKeyID: 91}) {
		t.Fatalf("内部服务身份=%+v，不符合预期", next.gotIdentity)
	}
	if !next.gotActorOK || next.gotActor != (adminActor{Source: admin.AdminSourceToken, ID: 41, Role: admin.RolePlatformAdmin}) {
		t.Fatalf("真实操作者=%+v，存在=%v，不符合预期", next.gotActor, next.gotActorOK)
	}
}

func TestAdminAuthMiddleware下级租户管理员默认无权使用(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID:       52,
		Source:        admin.AdminSourceToken,
		Role:          admin.RoleTenantOperator,
		ScopeTenantID: 7,
	}}
	deps, capabilities, principals := completeAdminDeps(resolver)
	capabilities.allowed = false
	rec, next := runAdminMiddleware(deps, "/conversations")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码=%d，响应=%s，期望 403", rec.Code, rec.Body.String())
	}
	if next.served || principals.called {
		t.Fatal("未获授权的租户管理员进入了 Hermes 或创建了服务主体")
	}
	if !capabilities.called || capabilities.tenantID != 7 || capabilities.capabilityKey != tenantcapability.HermesOperations {
		t.Fatalf("能力检查=%+v，不符合租户 7 的 Hermes 授权合同", capabilities)
	}
}

func TestAdminAuthMiddleware已授权租户管理员只能使用自身租户(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID:       53,
		Source:        admin.AdminSourceToken,
		Role:          admin.RoleTenantOperator,
		ScopeTenantID: 7,
	}}
	deps, capabilities, principals := completeAdminDeps(resolver)
	principals.principal = hermesprincipal.Principal{TenantID: 7, UserID: 82, APIKeyID: 92}
	rec, next := runAdminMiddleware(deps, "/conversations")
	if rec.Code != http.StatusOK || !next.served {
		t.Fatalf("状态码=%d，响应=%s，处理器到达=%v", rec.Code, rec.Body.String(), next.served)
	}
	if !capabilities.called || capabilities.tenantID != 7 || principals.tenantID != 7 {
		t.Fatalf("授权租户=%d，服务主体租户=%d，期望均为 7", capabilities.tenantID, principals.tenantID)
	}
	if next.gotIdentity != (sessionauth.Identity{TenantID: 7, UserID: 82, APIKeyID: 92}) {
		t.Fatalf("内部服务身份=%+v，不符合预期", next.gotIdentity)
	}
	if !next.gotActorOK || next.gotActor.ID != 53 || next.gotActor.Role != admin.RoleTenantOperator {
		t.Fatalf("真实操作者=%+v，存在=%v，不符合预期", next.gotActor, next.gotActorOK)
	}
}

func TestAdminAuthMiddleware管理员会话按用户归因(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		UserID: 67,
		Source: admin.AdminSourceSession,
		Role:   admin.RolePlatformAdmin,
	}}
	deps, _, _ := completeAdminDeps(resolver)
	rec, next := runAdminMiddleware(deps, "/tools")
	if rec.Code != http.StatusOK || !next.gotActorOK {
		t.Fatalf("状态码=%d，响应=%s，操作者存在=%v", rec.Code, rec.Body.String(), next.gotActorOK)
	}
	if next.gotActor != (adminActor{Source: admin.AdminSourceSession, ID: 67, Role: admin.RolePlatformAdmin}) {
		t.Fatalf("会话操作者=%+v，不符合预期", next.gotActor)
	}
}

func TestAdminAuthMiddleware能力后端异常时关闭入口(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID:       70,
		Source:        admin.AdminSourceToken,
		Role:          admin.RoleTenantOperator,
		ScopeTenantID: 7,
	}}
	deps, capabilities, principals := completeAdminDeps(resolver)
	capabilities.err = errors.New("存储暂时不可用")
	rec, next := runAdminMiddleware(deps, "/tools")
	if rec.Code != http.StatusServiceUnavailable || next.served || principals.called {
		t.Fatalf("状态码=%d，处理器到达=%v，服务主体调用=%v", rec.Code, next.served, principals.called)
	}
}

func TestAdminAuthMiddleware服务主体异常时关闭入口(t *testing.T) {
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 71,
		Source:  admin.AdminSourceToken,
		Role:    admin.RolePlatformAdmin,
	}}
	deps, _, principals := completeAdminDeps(resolver)
	principals.err = errors.New("服务主体暂时不可用")
	rec, next := runAdminMiddleware(deps, "/tools")
	if rec.Code != http.StatusServiceUnavailable || next.served {
		t.Fatalf("状态码=%d，处理器到达=%v，期望关闭入口", rec.Code, next.served)
	}
}
