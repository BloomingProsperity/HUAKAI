package gatewayhttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// UC-07:重发验证邮件端点。① 待验证用户重发能签发新 token 并发信;② 防枚举——不存在的
// 邮箱同样回 202 且不发信,与存在时的成功响应不可区分。
func mountResendAuthRouter(authSvc *userauth.Service, email AuthEmailSender) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, EmailSender: email})
	})
	return r
}

// TestAuthResendVerification_ResendsForPendingUser:注册出一个待验证用户后,对其邮箱调
// resend-verification → 202 且 SendVerification 被再次调用(新 token 发出)。
// 变异刀:删掉 MountAuthRoutes 里 /resend-verification 路由 → 请求得 404 → 转红。
func TestAuthResendVerification_ResendsForPendingUser(t *testing.T) {
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.RequireVerified = true
	authSvc.Now = func() time.Time { return now }
	email := &captureAuthEmail{}
	r := mountResendAuthRouter(authSvc, email)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "pending@example.test", "password": "secret12",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	first := email.verification
	if first == "" {
		t.Fatal("注册应发出首封验证邮件")
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]any{
		"tenant_id": 1, "email": "pending@example.test",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.verification == "" || email.verification == first {
		t.Fatalf("重发应签发并发出新验证 token;first=%q now=%q", first, email.verification)
	}
}

// TestAuthResendVerification_UnknownEmailDoesNotEnumerate:未注册邮箱调 resend → 同样 202
// 且不发信,与存在时不可区分(防枚举)。变异刀:若 handler 对不存在邮箱回 404/不同状态 → 转红。
func TestAuthResendVerification_UnknownEmailDoesNotEnumerate(t *testing.T) {
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.Now = func() time.Time { return now }
	email := &captureAuthEmail{}
	r := mountResendAuthRouter(authSvc, email)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]any{
		"tenant_id": 1, "email": "ghost@example.test",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.verification != "" {
		t.Fatalf("未注册邮箱不得发信(枚举侧信道);got token=%q", email.verification)
	}
}
