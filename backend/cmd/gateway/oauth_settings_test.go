package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type fakeOAuthSettings struct {
	values map[platformsettings.SettingKey]string
}

func (f fakeOAuthSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	return platformsettings.StoredSetting{Key: key, Value: f.values[key]}, nil
}

// TestOverlayOAuthSettings 判别 settings-first 逐字段覆盖:非空覆盖 env、空白保留 env、secret/scopes 覆盖。
// 变异(把某字段的覆盖删掉 / 去掉空白判定)→ 对应断言 RED。
func TestOverlayOAuthSettings(t *testing.T) {
	base := userauth.OAuthConfig{
		Provider:     "github",
		ClientID:     "env-id",
		ClientSecret: "env-secret",
		RedirectURI:  "env-cb",
		TokenURL:     "https://token.env",
		Scopes:       []string{"env-scope"},
	}

	// client_id 覆盖,未提及字段保留 env。
	got := overlayOAuthSettings(base, map[string]any{"client_id": "set-id"}, "", false)
	if got.ClientID != "set-id" {
		t.Fatalf("client_id 应被设置覆盖为 set-id,得 %q", got.ClientID)
	}
	if got.RedirectURI != "env-cb" || got.TokenURL != "https://token.env" {
		t.Fatalf("未覆盖字段应保留 env,得 redirect=%q token=%q", got.RedirectURI, got.TokenURL)
	}

	// 空白值不覆盖(保留 env)。
	if g := overlayOAuthSettings(base, map[string]any{"client_id": "   "}, "", false); g.ClientID != "env-id" {
		t.Fatalf("空白 client_id 应保留 env-id,得 %q", g.ClientID)
	}

	// secret 仅在 hasSec 且非空时覆盖。
	if g := overlayOAuthSettings(base, map[string]any{}, "set-secret", true); g.ClientSecret != "set-secret" {
		t.Fatalf("secret 应被覆盖为 set-secret,得 %q", g.ClientSecret)
	}
	if g := overlayOAuthSettings(base, map[string]any{}, "", true); g.ClientSecret != "env-secret" {
		t.Fatalf("空 secret 应保留 env-secret,得 %q", g.ClientSecret)
	}
	if g := overlayOAuthSettings(base, map[string]any{}, "ignored", false); g.ClientSecret != "env-secret" {
		t.Fatalf("hasSec=false 时不得动 secret,得 %q", g.ClientSecret)
	}

	// scopes 数组覆盖。
	if g := overlayOAuthSettings(base, map[string]any{"scopes": []any{"a", "b"}}, "", false); len(g.Scopes) != 2 || g.Scopes[0] != "a" {
		t.Fatalf("scopes 应被覆盖为 [a b],得 %v", g.Scopes)
	}

	// min_trust_level 数值覆盖(JSON 反序列化为 float64)+ trust_level_field 字符串覆盖。
	g := overlayOAuthSettings(base, map[string]any{"min_trust_level": float64(3), "trust_level_field": "tl"}, "", false)
	if g.MinimumNumericClaimValue != 3 {
		t.Fatalf("min_trust_level 应被覆盖为 3,得 %d", g.MinimumNumericClaimValue)
	}
	if g.MinimumNumericClaimField != "tl" {
		t.Fatalf("trust_level_field 应被覆盖为 tl,得 %q", g.MinimumNumericClaimField)
	}
	// 负数不覆盖(保留 env 基线 0)。
	if g := overlayOAuthSettings(base, map[string]any{"min_trust_level": float64(-1)}, "", false); g.MinimumNumericClaimValue != 0 {
		t.Fatalf("负 min_trust_level 不应覆盖,得 %d", g.MinimumNumericClaimValue)
	}
}

// TestOAuthResolverFallsBackWhenSettingsEmpty:设置为空时 resolver 返回 false → 调用方回退 env 静态。
// 变异(把 (!hasCfg && !hasSec) 短路删掉)→ 会尝试构建裸 provider → 行为改变 → 本断言 RED。
func TestOAuthResolverFallsBackWhenSettingsEmpty(t *testing.T) {
	resolve := oauthProviderSettingsResolver(fakeOAuthSettings{values: map[platformsettings.SettingKey]string{}}, nil)
	if _, ok := resolve(context.Background(), "github"); ok {
		t.Fatal("设置为空时 resolver 应返回 false(回退 env 静态 provider)")
	}
}

// TestOAuthResolverBuildsFromSettings:仅凭后台设置(env 无该 provider)也能现构可用 provider。
func TestOAuthResolverBuildsFromSettings(t *testing.T) {
	settings := fakeOAuthSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyOAuthProvidersConfig: `{"github":{"client_id":"gh-id","auth_url":"https://gh.test/authorize"}}`,
	}}
	resolve := oauthProviderSettingsResolver(settings, nil)
	p, ok := resolve(context.Background(), "github")
	if !ok || p == nil {
		t.Fatal("github 有后台设置应现构 provider")
	}
	if p.Provider() != "github" {
		t.Fatalf("provider 名应为 github,得 %q", p.Provider())
	}
}

// TestOAuthResolverSettingsOverrideEnv:env 与 settings 都配了 github client_id 时,以 settings 为准
//(settings-first)。用 AuthorizationURL 里的 client_id 做端到端判别。
// 变异(overlay 不覆盖 client_id / resolver 不走 settings)→ URL 里会是 env-gh → 本断言 RED。
func TestOAuthResolverSettingsOverrideEnv(t *testing.T) {
	t.Setenv("HUAKAI_GITHUB_OAUTH_CLIENT_ID", "env-gh")
	settings := fakeOAuthSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyOAuthProvidersConfig: `{"github":{"client_id":"set-gh","auth_url":"https://gh.test/authorize"}}`,
	}}
	resolve := oauthProviderSettingsResolver(settings, nil)
	p, ok := resolve(context.Background(), "github")
	if !ok || p == nil {
		t.Fatal("github 应现构 provider")
	}
	challenge, err := userauth.NewOAuthFlowChallenge(1, "github", "", time.Minute, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("构造 challenge: %v", err)
	}
	authURL, err := p.AuthorizationURL(challenge)
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	if !strings.Contains(authURL, "client_id=set-gh") {
		t.Fatalf("授权 URL 应含 settings 的 client_id=set-gh,得 %q", authURL)
	}
	if strings.Contains(authURL, "env-gh") {
		t.Fatalf("授权 URL 不应含 env 的 client_id,得 %q", authURL)
	}
}

// TestOAuthServiceProviderCtxPrefersResolver:ProviderCtx 先用注入的 resolver,未命中回退静态。
// 变异(ProviderCtx 跳过 resolver 直接走静态)→ 第一断言 RED。
func TestOAuthServiceProviderCtxPrefersResolver(t *testing.T) {
	svc := userauth.NewOAuthService()
	// 注入一个只认 github 的 resolver;返回一个纯设置驱动的 provider。
	settings := fakeOAuthSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyOAuthProvidersConfig: `{"github":{"client_id":"gh-id"}}`,
	}}
	svc.SetProviderResolver(oauthProviderSettingsResolver(settings, nil))
	if p, ok := svc.ProviderCtx(context.Background(), "github"); !ok || p == nil {
		t.Fatal("ProviderCtx 应经 resolver 命中 github")
	}
	// resolver 未命中的 provider(env 也没配)→ 静态兜底也没有 → false。
	if _, ok := svc.ProviderCtx(context.Background(), "google"); ok {
		t.Fatal("google 既无设置也无静态 provider,应返回 false")
	}
}
