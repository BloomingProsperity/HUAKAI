// HUAKAI · iKun
package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

// 凭证日常增改放开给登录 admin(Owner 批)。复用同包 stub + 共享 Resolver。
// 对照组:旧的 OAuth 采集流(acquisition)尚未挂 SessionSafe,session 写必须仍 401。
// 账号批量导入已按三身份能力授权合同开放给登录管理员。
func TestAdminCredentialSessionSafeWriteGate(t *testing.T) {
	mount := func() http.Handler {
		r := chi.NewRouter()
		d := AdminCredentialDeps{
			Auth: adminsessionauthtest.Resolver(), Credentials: &adminCredentialStoreStub{}, AuditStore: &adminPoolStoreStub{},
		}
		r.Route("/provider-accounts", func(r chi.Router) { MountAdminCredentialRoutes(r, d) })
		return r
	}
	// 放开的日常写端点:session-admin 过鉴权(≠401)。
	safeRoutes := []struct{ m, p string }{
		{http.MethodPost, "/provider-accounts/77/credentials"},
		{http.MethodPost, "/provider-accounts/77/credentials/5/rotate"},
		{http.MethodPatch, "/provider-accounts/77/credentials/5/state"},
		{http.MethodDelete, "/provider-accounts/77/credentials/5"},
	}
	h := mount()
	for _, tc := range safeRoutes {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
	// token 通道恒豁免。
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/provider-accounts/77/credentials/5", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌删凭证应过鉴权(≠401),得 401")
	}
	// 只读不受影响。
	if code := adminsessionauthtest.Status(h, http.MethodGet, "/provider-accounts/77/credentials", adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
		t.Fatalf("GET credentials(只读)应放行,得 401")
	}
}

// 账号采集写路由在三身份范围与 capability 守卫下允许管理会话操作。
// 浏览器 GET callback 不依赖会话，它只推进已授权创建且带固定 actor/tenant/account 的 flow。
func TestAdminCredentialAcquisitionAllowsAuthorizedSession(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminsessionauthtest.Resolver())
	for _, p := range []string{
		"/v1/admin/pool-accounts/101/credential-acquisitions",
		"/v1/admin/pool-accounts/101/credential-acquisitions/9/callback",
		"/v1/admin/pool-accounts/101/credential-acquisitions/9/cancel",
		"/v1/admin/pool-accounts/101/credential-acquisitions/9/finalize",
		"/admin/v1/credentials/paste",
		"/admin/v1/credentials/cli-import",
		"/admin/v1/credentials/csv-import",
		"/admin/v1/credentials/json-import",
		"/admin/v1/credentials/oauth-init",
		"/admin/v1/credentials/account-imports/plan",
		"/admin/v1/credentials/account-imports/execute",
	} {
		if code := adminsessionauthtest.Status(fx.handler, http.MethodPost, p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("能力授权后的账号采集 POST %s 应过 session 鉴权,得 401", p)
		}
	}
	// token 通道照常(过鉴权层;后续 400 等属业务校验,非鉴权拒)。
	if code := adminsessionauthtest.Status(fx.handler, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌走采集流应过鉴权(≠401),得 401")
	}
}
