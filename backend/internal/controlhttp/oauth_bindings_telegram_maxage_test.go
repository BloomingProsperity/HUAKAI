package controlhttp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// verifiedSocialBinderStub 记录绑定腿是否被调用,让「已签名但过期的 widget 是否被接受」可观测:
// 若过期载荷被 handler 接受 → LinkVerifiedSocialIdentity 被调 → calls>0(不安全);安全则 calls==0。
type verifiedSocialBinderStub struct {
	calls    int
	identity userauth.VerifiedIdentity
}

func (s *verifiedSocialBinderStub) LinkVerifiedSocialIdentity(_ context.Context, _, _ int64, identity userauth.VerifiedIdentity) (userauth.User, error) {
	s.calls++
	s.identity = identity
	return userauth.User{}, nil
}

// signTelegramWidget 用 bot token 按 Telegram data-check-string 规则算 HMAC-SHA256,产出一份「真实有效签名」的
// widget 参数集。签名有效意味着:唯一能拒绝它的只有 auth_date 时效门 —— 正是本测试要验证的那道门。
func signTelegramWidget(botToken string, params map[string]string) map[string]string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+params[k])
	}
	secret := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(strings.Join(lines, "\n")))
	out := make(map[string]string, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out["hash"] = hex.EncodeToString(mac.Sum(nil))
	return out
}

func telegramBindBody(t *testing.T, params map[string]string) string {
	t.Helper()
	b, err := json.Marshal(oauthBindingsTelegramRequest{Params: params})
	if err != nil {
		t.Fatalf("marshal telegram bind body: %v", err)
	}
	return string(b)
}

// 判别测试(安全行为):装配 OAuthBindingsDeps 时【未设 TelegramWidgetMaxAge】(=0,即生产 routes.go:338 的实况),
// 一份 HMAC 完全有效、但 auth_date 早已过期(100 天前)的 widget 载荷,必须被拒(401),且绝不落到绑定 service。
//
// 期望:handler/verifier 应对未配置的 maxAge 施加安全默认(24h),让过期的已签名载荷无法无限重放。
// 当前代码(RED):handler 把 0 直传 VerifyWidget,年龄门 `if maxAge>0` 永不触发 → 过期载荷被接受 →
//
//	binder.calls==1、状态 200 → 断言 401/未调用变红,证明重放漏洞真实存在。
func TestOAuthBindingsTelegramRejectsStaleWidgetWhenMaxAgeUnset(t *testing.T) {
	const botToken = "999999:bind-bot-secret"
	// auth_date 定在 100 天前:无论测试何时运行,都远超任何合理的 24h 默认时效窗口。
	staleAuth := time.Now().Add(-100 * 24 * time.Hour).Unix()
	params := signTelegramWidget(botToken, map[string]string{
		"id":        "700700",
		"username":  "replay_victim",
		"auth_date": strconv.FormatInt(staleAuth, 10),
	})

	binder := &verifiedSocialBinderStub{}
	// 关键:故意不设 TelegramWidgetMaxAge —— 复现生产装配缺陷。
	deps := OAuthBindingsDeps{
		TelegramBinder:   binder,
		TelegramBotToken: botToken,
	}
	rec := serveOAuthBindings(t, deps,
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "family-1"},
		http.MethodPost, "/v1/users/me/oauth-bindings/telegram", telegramBindBody(t, params))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 (stale signed widget must be rejected) body=%s", rec.Code, rec.Body.String())
	}
	if binder.calls != 0 {
		t.Fatalf("binder called %d times on stale widget; want 0 — expired payload replayed into binding (infinite replay vuln)", binder.calls)
	}
}

// 反向保护(不应过度修复):同样【未设 TelegramWidgetMaxAge】,一份 auth_date 为「此刻」的新鲜且签名有效的
// widget 必须仍能绑定成功(200 + binder 被调),证明默认时效只拒过期、不误伤正常绑定。
func TestOAuthBindingsTelegramAcceptsFreshWidgetWhenMaxAgeUnset(t *testing.T) {
	const botToken = "999999:bind-bot-secret"
	params := signTelegramWidget(botToken, map[string]string{
		"id":        "700700",
		"username":  "legit_user",
		"auth_date": strconv.FormatInt(time.Now().Unix(), 10),
	})

	binder := &verifiedSocialBinderStub{}
	deps := OAuthBindingsDeps{
		TelegramBinder:   binder,
		TelegramBotToken: botToken,
	}
	rec := serveOAuthBindings(t, deps,
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "family-1"},
		http.MethodPost, "/v1/users/me/oauth-bindings/telegram", telegramBindBody(t, params))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (fresh signed widget must still bind) body=%s", rec.Code, rec.Body.String())
	}
	if binder.calls != 1 {
		t.Fatalf("binder called %d times on fresh widget; want 1", binder.calls)
	}
	if binder.identity.Subject != "700700" {
		t.Fatalf("bound subject=%q want 700700", binder.identity.Subject)
	}
}
