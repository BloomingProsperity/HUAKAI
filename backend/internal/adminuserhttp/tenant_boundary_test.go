package adminuserhttp

import (
	"net/http"
	"strings"
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// tenant_boundary_test.go — 三身份边界(UC-01):部署者(platform_admin)只管理平台自有
// 租户的终端用户;下级租户用户只归其租户管理员,对部署者一律 403(对下级租户仅保留
// 余额调整口,走 balanceledger,不经本包)。

// TestPlatformAdminForbiddenOnSubTenantUsers:注入 PlatformTenantID=1 后,platform_admin
// 带 ?tenant_id=2(下级租户)访问用户管理端点必须 403,且不触达 store。
// 变异刀:删掉 tenantFromQueryOrScope 里的 PlatformTenantID 归属守卫(或把条件翻成恒放行)
// → 本测试拿 200 → 红。
func TestPlatformAdminForbiddenOnSubTenantUsers(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            store,
		PlatformTenantID: 1,
	}, http.MethodGet, "/admin/v1/users/101?tenant_id=2", "")
	assertStatus(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), "cross_tenant_user_admin_forbidden") {
		t.Fatalf("body=%s want cross_tenant_user_admin_forbidden", rec.Body.String())
	}
	if store.getCalls != 0 {
		t.Fatalf("越界请求不得触达 store,getCalls=%d", store.getCalls)
	}
}

// TestPlatformAdminAllowedOnPlatformTenantUsers:同样注入 PlatformTenantID=1,
// platform_admin 管平台自有租户(?tenant_id=1)照常放行——守卫只挡下级租户,
// 不收回单租户开箱即用行为。
func TestPlatformAdminAllowedOnPlatformTenantUsers(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            store,
		PlatformTenantID: 1,
	}, http.MethodGet, "/admin/v1/users/101?tenant_id=1", "")
	assertStatus(t, rec, http.StatusOK)
	if store.getArg.TenantID != 1 {
		t.Fatalf("plat tenant want 1, got %d", store.getArg.TenantID)
	}
}

func TestPlatformAdminFailsClosedWithoutPlatformTenant(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:  usersAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodGet, "/admin/v1/users/101?tenant_id=1", "")
	assertStatus(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "platform_tenant_not_configured") {
		t.Fatalf("body=%s want platform_tenant_not_configured", rec.Body.String())
	}
	if store.getCalls != 0 {
		t.Fatalf("归属无法证明时不得触达 store,getCalls=%d", store.getCalls)
	}
}
