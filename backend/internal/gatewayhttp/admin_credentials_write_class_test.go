// HUAKAI · iKun
package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

// 凭证日常增改放开给登录 admin(Owner 批)。复用同包 stub + 共享 Resolver。
// 对照组:OAuth 采集流(acquisition)未挂 SessionSafe,session 写必须仍 401(采集流留 token-only)。
// 变异:摘任一 .With(safe) → 对应 session 写 401 → RED;给 acquisition 挂 safe → 对照断言 RED。
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

// 采集流对照:acquisition 写路由(未挂 SessionSafe)session 仍 401。
// 这是「日常改动放开、采集流(含 mutating-GET callback 债)不放」的边界锁。
// 用既有 fixture(全依赖 stub)确保打到鉴权层而非依赖判空的 503。
func TestAdminCredentialAcquisitionStaysTokenOnly(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminsessionauthtest.Resolver())
	// canonical 流 4 条写 + import-helper 5 条(paste/cli/csv/json/oauth-init)全部锁死:
	// helper 是「录入凭证」的另一条完整通路(且支持批量),错误放开任意一条都要红。
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
		if code := adminsessionauthtest.Status(fx.handler, http.MethodPost, p, adminsessionauthtest.SessionBearer); code != http.StatusUnauthorized {
			t.Fatalf("采集流 POST %s 应仍 token-only(session 401),得 %d", p, code)
		}
	}
	// token 通道照常(过鉴权层;后续 400 等属业务校验,非鉴权拒)。
	if code := adminsessionauthtest.Status(fx.handler, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌走采集流应过鉴权(≠401),得 401")
	}
}
