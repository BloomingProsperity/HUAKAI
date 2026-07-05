// HUAKAI · iKun

// 2FA 登录完成路径的账号资格门测试: challenge 签发(密码对)到提交验证码之间账号被
// 封禁/锁定/删除, 完成步必须拒签会话 —— 否则 challenge 窗口成为停用控制的绕过口。

package gatewayhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// twoFactorLoginStub 直接放行 challenge 校验 (身份由 result 给定), 隔离测资格门本身。
type twoFactorLoginStub struct {
	result twofa.VerifyResult
	err    error
}

func (s twoFactorLoginStub) LoginRequired(context.Context, int64, int64) (bool, error) {
	return true, nil
}

func (s twoFactorLoginStub) StartLoginChallenge(context.Context, int64, int64) (twofa.Challenge, error) {
	return twofa.Challenge{}, nil
}

func (s twoFactorLoginStub) VerifyLoginChallenge(context.Context, twofa.ChallengeVerifyInput) (twofa.VerifyResult, error) {
	return s.result, s.err
}

// TestAuthTwoFactorLogin_RejectsIneligibleUser 守资格门: 验证码通过但账号已被
// 封禁/时间锁/删除 → 403 account_not_active 且不签发会话; 账号正常 → 200 签发。
// mutation: 去掉 handler 里 GetProfile+EnsureLoginEligible 门 → disabled/locked/deleted
// 三例返回 200 带会话 → 红。
func TestAuthTwoFactorLogin_RejectsIneligibleUser(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	lockedUntil := now.Add(30 * time.Minute)

	cases := []struct {
		name       string
		mutate     func(u *userauth.User)
		wantStatus int
		wantCode   string
	}{
		{"active 正常签发", func(u *userauth.User) {}, http.StatusOK, ""},
		{"disabled 拒签", func(u *userauth.User) { u.Status = userauth.UserStatusDisabled }, http.StatusForbidden, "account_not_active"},
		{"时间锁拒签", func(u *userauth.User) { u.LockedUntil = &lockedUntil }, http.StatusForbidden, "account_not_active"},
		{"已删用户拒签", nil, http.StatusForbidden, "account_not_active"}, // mutate=nil: 不种用户 (GetUserByID → ErrUserNotFound)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authStore := newGatewayMemoryAuthStore(now)
			if tc.mutate != nil {
				user := userauth.User{ID: 1001, TenantID: 1, Email: "u@example.test", Status: userauth.UserStatusActive}
				tc.mutate(&user)
				authStore.users[1001] = user
			}
			authSvc := userauth.NewService(authStore)
			authSvc.Now = func() time.Time { return now }
			sessionSvc := usersession.NewService(usersession.NewMemoryStore())
			sessionSvc.Now = func() time.Time { return now }
			sessionSvc.SigningKey = testSessionSigningKey()

			r := chi.NewRouter()
			r.Route("/v1/auth", func(r chi.Router) {
				MountAuthRoutes(r, AuthHandlerDeps{
					Auth: authSvc, Sessions: sessionSvc,
					TwoFactor: twoFactorLoginStub{result: twofa.VerifyResult{TenantID: 1, UserID: 1001, Method: twofa.MethodTOTP}},
				})
			})

			rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login/2fa", map[string]any{
				"challenge_id": "ch-1", "code": "123456",
			})
			assertHTTPStatus(t, rec, tc.wantStatus)
			if tc.wantCode != "" {
				if code := loginErrorCode(t, rec); code != tc.wantCode {
					t.Fatalf("error code = %q want %q", code, tc.wantCode)
				}
			}
		})
	}
}
