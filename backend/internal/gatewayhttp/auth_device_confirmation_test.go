package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// TestDeviceConfirmationEndToEnd_LoginBlockedSendsEmailThenConfirmFreesSlot 覆盖 confirm 流的
// HTTP 端到端: 设备达上限的 password 登录 → 403 device_confirmation_required + 发确认邮件 +
// 响应体绝不含 token → 带邮件里的 token 调 /v1/auth/confirm-device → 200 device_confirmed →
// 重新登录 → 200 成功 (最老 family 已被腾位)。
//
// 变异 (§14):
//   - 把 auth_handler 里 4 个 Create 调用点的 `handleDeviceConfirmationRequired(...) { return }` 删掉
//     → 登录会落到默认 writeSessionError 的 503 (ErrDeviceConfirmationRequired 走 default 分支),
//     "want 403" 断言变红, 且 email.deviceConfirmation 仍为空 ("confirmation email not sent" 变红)。
//   - 把响应体 device_confirmation_required 改成回显 token → "response body must not contain token" 变红。
//     (已手动验证其一: 临时删掉 password 登录点的 handleDeviceConfirmationRequired 分支 → status=503 报红; 还原后绿。)
func TestDeviceConfirmationEndToEnd_LoginBlockedSendsEmailThenConfirmFreesSlot(t *testing.T) {
	now := time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }

	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	// 上限 1 + confirm 策略: 用户已有 1 个活跃 family 后再登录即触发确认流。
	sessionSvc.MaxActiveFamilies = 1
	sessionSvc.DevicePolicy = "confirm"

	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc, EmailSender: email})
	})

	// 注册 + 验证邮箱。
	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "dev@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]any{
		"tenant_id": 1, "token": email.verification,
	})
	assertHTTPStatus(t, rec, http.StatusOK)

	// 取 userID, 直接给该用户预置 1 个活跃 family 把它顶到上限。
	userID := userIDByEmail(t, authStore, "dev@example.test")
	if _, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 1, UserID: userID, IP: "10.0.0.9", UserAgent: "Old/1",
	}); err != nil {
		t.Fatalf("seed existing family: %v", err)
	}

	// 第二台设备登录: 应被确认流拦截。
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "dev@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusForbidden)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode 403 body: %v", err)
	}
	if errBody.Error.Code != "device_confirmation_required" {
		t.Fatalf("error.code=%q want device_confirmation_required body=%s", errBody.Error.Code, rec.Body.String())
	}
	// 确认邮件已发, 且响应体绝不含原文 token。
	if email.deviceConfirmation == "" {
		t.Fatal("device confirmation email was not sent")
	}
	if strings.Contains(rec.Body.String(), email.deviceConfirmation) {
		t.Fatalf("response body must not contain the confirmation token; body=%s", rec.Body.String())
	}

	// 带邮件里的 token 确认设备。
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/confirm-device", map[string]any{
		"tenant_id": 1, "token": email.deviceConfirmation,
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "device_confirmed") {
		t.Fatalf("confirm response missing device_confirmed: %s", rec.Body.String())
	}

	// 腾位后重新登录: 应成功 (最老 family 被撤, 名额空出 1)。
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "dev@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
}

// TestConfirmDeviceEndpoint_RejectsInvalidToken 覆盖确认端点对无效 token 的 401 映射 (枚举防御:
// 不存在 / 已用同响应)。
func TestConfirmDeviceEndpoint_RejectsInvalidToken(t *testing.T) {
	now := time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()

	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Sessions: sessionSvc})
	})

	// 缺 token → 400。
	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/confirm-device", map[string]any{"tenant_id": 1})
	assertHTTPStatus(t, rec, http.StatusBadRequest)

	// 未注册的随机 token → 401。
	wrong, _, err := usersession.GenerateDeviceConfirmationToken()
	if err != nil {
		t.Fatalf("GenerateDeviceConfirmationToken: %v", err)
	}
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/confirm-device", map[string]any{"tenant_id": 1, "token": wrong})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
}

func userIDByEmail(t *testing.T, store *gatewayMemoryAuthStore, email string) int64 {
	t.Helper()
	user, err := store.GetUserByEmail(context.Background(), 1, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	return user.ID
}
