package antigravity

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestAntigravityOAuthConfigRequiresOperatorVerifiedPKCEConfig(t *testing.T) {
	// 已堵回归:Antigravity 的 OAuth endpoint、client ID 和 scope 还不是公开
	// 契约输入。变异自检:硬编码任意默认 endpoint/client/scope 都会让本测试变红。
	cfg := DefaultOAuthConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" {
		t.Fatalf("default Antigravity OAuth cfg must be operator-gated, got auth=%q token=%q client=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default Antigravity OAuth scopes=%v, want none until operator config", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrAntigravityOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrAntigravityOAuthConfigRequired", err)
	}
}

func TestAntigravityOAuthAuthorizeURLUsesOperatorPKCEValues(t *testing.T) {
	// 已堵回归:PKCE 授权 URL 必须仅由 operator 配置构建。变异自检:臆测的
	// Antigravity endpoint 或 scope 会产出不同的 URL/query 并在此处失败。
	override := credentialacq.OAuthClientConfig{
		AuthURL:     "https://operator.antigravity.example.test/oauth/authorize",
		TokenURL:    "https://operator.antigravity.example.test/oauth/token",
		ClientID:    "antigravity-client-id",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes:      []string{"openid", "email", "offline_access"},
	}
	cfg := OAuthConfig(override)
	override.Scopes[0] = "mutated"

	authURL, err := BuildOAuthAuthorizeURL(cfg, "state-value", "challenge-value")
	if err != nil {
		t.Fatalf("BuildOAuthAuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://operator.antigravity.example.test/oauth/authorize" {
		t.Fatalf("authorize endpoint=%q", got)
	}
	q := parsed.Query()
	assertAntigravityQueryValue(t, q, "response_type", "code")
	assertAntigravityQueryValue(t, q, "client_id", "antigravity-client-id")
	assertAntigravityQueryValue(t, q, "redirect_uri", "http://127.0.0.1:1455/auth/callback")
	assertAntigravityQueryValue(t, q, "state", "state-value")
	assertAntigravityQueryValue(t, q, "code_challenge", "challenge-value")
	assertAntigravityQueryValue(t, q, "code_challenge_method", AntigravityPKCEMethodS256)
	assertAntigravityQueryValue(t, q, "scope", "openid email offline_access")
}

func TestAntigravityOAuthConfigRejectsNonOperatorSource(t *testing.T) {
	// 已堵回归:source=operator_config 是强制校验的,不是装饰性的。
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:     "https://operator.antigravity.example.test/oauth/authorize",
		TokenURL:    "https://operator.antigravity.example.test/oauth/token",
		ClientID:    "antigravity-client-id",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes:      []string{"openid", "offline_access"},
		Source:      credentialacq.ClientSourcePublicCLI,
	})
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("OAuthConfig source=%q, want forced operator_config", cfg.Source)
	}

	raw := cfg
	raw.Source = credentialacq.ClientSourcePublicCLI
	if err := ValidateOAuthConfig(raw); !errors.Is(err, ErrAntigravityOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrAntigravityOAuthConfigRequired", err)
	}
}

func TestAntigravityRefreshAdapterFromOAuthConfigUsesOperatorConfig(t *testing.T) {
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:      "https://operator.antigravity.example.test/oauth/authorize",
		TokenURL:     "https://operator.antigravity.example.test/oauth/token",
		ClientID:     "antigravity-client-id",
		ClientSecret: "antigravity-client-secret",
		RedirectURI:  "http://127.0.0.1:1455/auth/callback",
		Scopes:       []string{"openid", "email", "offline_access"},
	})

	adapter, err := RefreshAdapterFromOAuthConfig(cfg)
	if err != nil {
		t.Fatalf("RefreshAdapterFromOAuthConfig: %v", err)
	}
	if adapter.TokenURL != "https://operator.antigravity.example.test/oauth/token" || adapter.ClientID != "antigravity-client-id" {
		t.Fatalf("adapter endpoint/client=(%q,%q), want operator values", adapter.TokenURL, adapter.ClientID)
	}
	if adapter.ClientSecret != "antigravity-client-secret" {
		t.Fatalf("adapter client_secret=%q, want operator secret", adapter.ClientSecret)
	}
	if got := adapter.Scope; got != "openid email offline_access" {
		t.Fatalf("scope=%q, want operator scopes", got)
	}
}

func assertAntigravityQueryValue(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("query %s=%q, want %q; all=%v", key, got, want, q)
	}
}
