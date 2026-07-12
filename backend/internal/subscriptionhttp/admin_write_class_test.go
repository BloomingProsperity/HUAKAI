package subscriptionhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

// money-via-login Stage 5:subscriptionhttp admin 动钱端点放开给登录 admin(session)。
// 复用既有 adminOpsServiceStub(全 Service 实现)+ 共享 Resolver。挂在 /subs 下。
// 变异:摘任一路由的 .With(safe) → session 写 401 → 对应断言 RED。
func TestSubscriptionAdminSessionSafeWriteGate(t *testing.T) {
	mount := func() http.Handler {
		r := chi.NewRouter()
		d := AdminDeps{Auth: adminsessionauthtest.Resolver(), Service: &adminOpsServiceStub{}}
		r.Route("/subs", func(r chi.Router) { MountSubscriptionAdminRoutes(r, d) })
		return r
	}
	safeRoutes := []struct{ m, p string }{
		{http.MethodPost, "/subs/plans"},
		{http.MethodPut, "/subs/plans/5"},
		{http.MethodPost, "/subs/plans/5/disable"},
		{http.MethodPost, "/subs/assignments"},
		{http.MethodPost, "/subs/assignments/5/extend"},
		{http.MethodPost, "/subs/assignments/5/revoke"},
	}
	h := mount()
	for _, tc := range safeRoutes {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
	if code := adminsessionauthtest.Status(h, http.MethodPost, "/subs/assignments", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌 POST /subs/assignments 应过鉴权(≠401),得 401")
	}
	if code := adminsessionauthtest.Status(h, http.MethodGet, "/subs/plans", adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
		t.Fatalf("GET /subs/plans(只读)应放行,得 401")
	}
}
