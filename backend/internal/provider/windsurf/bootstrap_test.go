package windsurf

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestWindsurfOAuthConfigRequiresOperatorVerifiedEndpoints(t *testing.T) {
	// 守住的回归：Windsurf 的 OAuth endpoints/client/scopes 绝不能靠猜。
	// 变异自检：在 Owner 抓取/溯源建立之前，硬编码任何默认 endpoint、client_id
	// 或 scope 都会让本测试失败。
	cfg := DefaultOAuthConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" {
		t.Fatalf("default Windsurf OAuth cfg must be operator-gated, got auth=%q token=%q client=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default Windsurf OAuth scopes=%v, want none until capture/Owner confirmation", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrWindsurfOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrWindsurfOAuthConfigRequired", err)
	}
}

func TestWindsurfOAuthAuthorizeURLUsesConfiguredPKCES256(t *testing.T) {
	// 守住的回归：operator 提供的 Windsurf OAuth 值必须流入 PKCE authorize URL，
	// 不依赖任何未经验证的默认值。
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:     "https://windsurf-oauth.example.test/authorize",
		TokenURL:    "https://windsurf-oauth.example.test/token",
		ClientID:    "windsurf-client-id",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes:      []string{"openid", "offline_access"},
	})

	authURL, err := BuildOAuthAuthorizeURL(cfg, "state-value", "challenge-value")
	if err != nil {
		t.Fatalf("BuildOAuthAuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://windsurf-oauth.example.test/authorize" {
		t.Fatalf("authorize endpoint=%q", got)
	}
	q := parsed.Query()
	assertQueryValue(t, q, "response_type", "code")
	assertQueryValue(t, q, "client_id", "windsurf-client-id")
	assertQueryValue(t, q, "redirect_uri", "http://127.0.0.1:1455/auth/callback")
	assertQueryValue(t, q, "state", "state-value")
	assertQueryValue(t, q, "code_challenge", "challenge-value")
	assertQueryValue(t, q, "code_challenge_method", WindsurfPKCEMethodS256)
	assertQueryValue(t, q, "scope", "openid offline_access")
}

func assertQueryValue(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("query %s=%q, want %q; all=%v", key, got, want, q)
	}
}
