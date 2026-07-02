package paymenthttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

// money-via-login Stage 5:paymenthttp admin 动钱端点放开给登录 admin(session)。
// 复用既有 captureService(全 Service 实现)+ 共享 Resolver。挂在 /orders 下(同 gateway 形态)。
// 变异:摘任一路由的 .With(safe) → session 写 401 → 对应断言 RED。
func TestPaymentAdminSessionSafeWriteGate(t *testing.T) {
	mount := func() http.Handler {
		r := chi.NewRouter()
		d := AdminDeps{Auth: adminsessionauthtest.Resolver(), Service: &captureService{}}
		r.Route("/orders", func(r chi.Router) { MountPaymentAdminRoutes(r, d) })
		return r
	}
	// 代表性动钱写端点(退款/取消/确认/建单):session-admin 过鉴权(≠401)。
	safeRoutes := []struct{ m, p string }{
		{http.MethodPost, "/orders/"},
		{http.MethodPost, "/orders/5/refund"},
		{http.MethodPost, "/orders/5/cancel"},
		{http.MethodPost, "/orders/5/confirm"},
		{http.MethodPost, "/orders/5/retry"},
	}
	h := mount()
	for _, tc := range safeRoutes {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
	// token 豁免。
	if code := adminsessionauthtest.Status(h, http.MethodPost, "/orders/5/refund", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌退款应过鉴权(≠401),得 401")
	}
	// 只读端点不受影响:GET /orders 恒放行(session-admin)。
	if code := adminsessionauthtest.Status(h, http.MethodGet, "/orders/", adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
		t.Fatalf("GET /orders(只读)应放行,得 401")
	}
}
