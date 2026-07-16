package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// 守 adminGate 注入已认证身份到 context: 放行后下游 handler 可经 admin.IdentityFromContext 读回真实身份,
// 据此做审计/归属(取代信任请求体的归属字段, 防伪造)。
// mutation: adminGate 不 r.WithContext(IdentityToContext) 注入 → 下游 ok=false → 本测红。
func TestAdminGateInjectsIdentityIntoContext(t *testing.T) {
	resolver := fakeAdminResolver{id: admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}}
	var gotID admin.AdminIdentity
	var gotOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, gotOK = admin.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	gated := adminGate(resolver, inner)

	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("platform_admin 应放行 200, 实际 %d", rec.Code)
	}
	if !gotOK {
		t.Fatal("adminGate 放行后下游应能读回身份, 但 ok=false(未注入 context)")
	}
	if gotID.TokenID != 99 || gotID.Role != admin.RolePlatformAdmin {
		t.Fatalf("下游读回身份=%+v, want TokenID=99 / platform_admin", gotID)
	}
}

// 守注入【在 RBAC 之后】: tenant_operator 被 403 拒, 下游 handler 根本不被调用(自然也不会注入身份)。
// mutation: 把身份注入误移到 RBAC 检查之前并放行 → forbidden 请求也触下游 → innerCalled=true → 红。
func TestAdminGateDoesNotReachHandlerOnForbidden(t *testing.T) {
	resolver := fakeAdminResolver{id: admin.AdminIdentity{TokenID: 7, Role: admin.RoleTenantOperator, ScopeTenantID: 42}}
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	gated := adminGate(resolver, inner)

	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant_operator 应 403, 实际 %d", rec.Code)
	}
	if innerCalled {
		t.Fatal("被 RBAC 拒时下游 handler 绝不应被调用")
	}
}
