package adminhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

// money-via-login Stage 5:充值/余额调整端点放开给登录 admin(session)——挂 AllowSessionWrite(SessionSafe)。
// 端到端:knob 开 session-admin 过鉴权(≠401,后续因空 body 400);knob 关回退令牌被拒 401;token 豁免。
// 变异:摘 MountBalanceCreditRoutes 的 .With(safe) → session 写 401 → 首断言 RED。
func TestBalanceCreditSessionSafeWriteGate(t *testing.T) {
	mount := func() http.Handler {
		r := chi.NewRouter()
		MountBalanceCreditRoutes(r, AdminBalanceCreditDeps{
			Auth:    adminsessionauthtest.Resolver(),
			Service: &balanceCreditServiceStub{},
		})
		return r
	}
	// SessionSafe + session-admin:过鉴权(≠401)。
	if code := adminsessionauthtest.Status(mount(), http.MethodPost, "/adjustments", adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
		t.Fatalf("SessionSafe POST /adjustments 应过鉴权(≠401),得 401")
	}
	// token 通道豁免:hk_admin 令牌过鉴权(≠401)。
	if code := adminsessionauthtest.Status(mount(), http.MethodPost, "/adjustments", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌 POST /adjustments 应过鉴权(≠401),得 401")
	}
}
