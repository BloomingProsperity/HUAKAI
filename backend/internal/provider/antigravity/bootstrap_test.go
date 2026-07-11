package antigravity

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

// TestAntigravityDefaultOAuthConfigPinsPublicCLIProfile 守住内置 client、secret、
// token endpoint 与五段 scope；任一常量退回空占位都会直接变红。
func TestAntigravityDefaultOAuthConfigPinsPublicCLIProfile(t *testing.T) {
	cfg := DefaultOAuthConfig()
	if cfg.ClientID != AntigravityOAuthClientID || cfg.ClientSecret != AntigravityOAuthClientSecret {
		t.Fatalf("内置 OAuth client 不匹配：id=%q secret=%q", cfg.ClientID, cfg.ClientSecret)
	}
	if cfg.TokenURL != AntigravityOAuthTokenEndpoint {
		t.Fatalf("token endpoint=%q", cfg.TokenURL)
	}
	if got := strings.Join(cfg.Scopes, " "); got != antigravityOAuthScope {
		t.Fatalf("scopes=%q，期望 %q", got, antigravityOAuthScope)
	}
	if cfg.Source != credentialacq.ClientSourcePublicCLI {
		t.Fatalf("source=%q，期望 public_cli_client", cfg.Source)
	}
	if _, err := RefreshAdapterFromOAuthConfig(cfg); err != nil {
		t.Fatalf("默认配置应可直接构造 refresh adapter：%v", err)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrAntigravityOAuthConfigRequired) || !strings.Contains(err.Error(), "auth_url") {
		t.Fatalf("未提供授权页时完整 PKCE 校验应 fail-closed，err=%v", err)
	}
}

func TestAntigravityOAuthConfigPinsIdentityAgainstOverrides(t *testing.T) {
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:      "https://accounts.example.test/o/oauth2/auth",
		TokenURL:     "https://attacker.example/token",
		ClientID:     "attacker-client",
		ClientSecret: "attacker-secret",
		RedirectURI:  "http://127.0.0.1:1455/auth/callback",
		Scopes:       []string{"attacker-scope"},
		Source:       credentialacq.ClientSourceOperatorConfig,
	})
	if cfg.TokenURL != AntigravityOAuthTokenEndpoint || cfg.ClientID != AntigravityOAuthClientID || cfg.ClientSecret != AntigravityOAuthClientSecret {
		t.Fatalf("OAuth 固定身份被 override 改写：%+v", cfg)
	}
	if got := strings.Join(cfg.Scopes, " "); got != antigravityOAuthScope {
		t.Fatalf("scope 被 override 改写：%q", got)
	}
	if cfg.Source != credentialacq.ClientSourcePublicCLI {
		t.Fatalf("source 被 override 改写：%q", cfg.Source)
	}

	authorizeURL, err := BuildOAuthAuthorizeURL(cfg, "state-value", "challenge-value")
	if err != nil {
		t.Fatalf("BuildOAuthAuthorizeURL 失败：%v", err)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("解析 authorize URL 失败：%v", err)
	}
	query := parsed.Query()
	assertAntigravityQueryValue(t, query, "client_id", AntigravityOAuthClientID)
	assertAntigravityQueryValue(t, query, "scope", antigravityOAuthScope)
	assertAntigravityQueryValue(t, query, "state", "state-value")
	assertAntigravityQueryValue(t, query, "code_challenge", "challenge-value")
	assertAntigravityQueryValue(t, query, "code_challenge_method", AntigravityPKCEMethodS256)
}

func TestAntigravityRefreshConfigRejectsTamperedProfile(t *testing.T) {
	for _, mutate := range []func(*credentialacq.OAuthClientConfig){
		func(cfg *credentialacq.OAuthClientConfig) { cfg.TokenURL = "https://attacker.example/token" },
		func(cfg *credentialacq.OAuthClientConfig) { cfg.ClientID = "attacker-client" },
		func(cfg *credentialacq.OAuthClientConfig) { cfg.ClientSecret = "attacker-secret" },
		func(cfg *credentialacq.OAuthClientConfig) { cfg.Scopes = []string{"attacker-scope"} },
		func(cfg *credentialacq.OAuthClientConfig) { cfg.Source = credentialacq.ClientSourceOperatorConfig },
	} {
		cfg := DefaultOAuthConfig()
		mutate(&cfg)
		if _, err := RefreshAdapterFromOAuthConfig(cfg); !errors.Is(err, ErrAntigravityOAuthConfigRequired) {
			t.Fatalf("被改写的公开 profile 应被拒绝，cfg=%+v err=%v", cfg, err)
		}
	}
}

func TestAntigravityRefreshAdapterUsesBuiltinProfile(t *testing.T) {
	adapter, err := RefreshAdapterFromOAuthConfig(DefaultOAuthConfig())
	if err != nil {
		t.Fatalf("RefreshAdapterFromOAuthConfig 失败：%v", err)
	}
	if adapter.TokenURL != AntigravityOAuthTokenEndpoint || adapter.ClientID != AntigravityOAuthClientID {
		t.Fatalf("adapter endpoint/client=(%q,%q)", adapter.TokenURL, adapter.ClientID)
	}
	if adapter.ClientSecret != AntigravityOAuthClientSecret || adapter.Scope != antigravityOAuthScope {
		t.Fatalf("adapter secret/scope 未使用内置 profile")
	}
}

func assertAntigravityQueryValue(t *testing.T, query url.Values, key, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("query %s=%q，期望 %q；all=%v", key, got, want, query)
	}
}
