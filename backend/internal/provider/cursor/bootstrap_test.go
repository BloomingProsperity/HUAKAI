package cursor

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestCursorOAuthConfigRequiresOperatorVerifiedEndpoints(t *testing.T) {
	// 消除的回归:当前允许的参考资料中并未核实过 Cursor OAuth endpoint。
	// 变异自检:若硬编码一个臆测的 endpoint、client_id 或 scope,本测试会失败,
	// 而不是让它静默上线。
	cfg := DefaultOAuthConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" {
		t.Fatalf("default Cursor OAuth cfg must be operator-gated, got auth=%q token=%q client=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default Cursor OAuth scopes=%v, want none until capture/Owner confirmation", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrCursorOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrCursorOAuthConfigRequired", err)
	}
}

func TestCursorOAuthConfigRejectsMissingEachOperatorField(t *testing.T) {
	// 变异守卫：一旦 ValidateOAuthConfig 不再检查
	// auth_url / token_url / client_id / redirect_uri 中的任意一个,对应子测试就会变红。
	fullValid := credentialacq.OAuthClientConfig{
		AuthURL:     "https://cursor-oauth.example.test/authorize",
		TokenURL:    "https://cursor-oauth.example.test/token",
		ClientID:    "cursor-client-id",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
	}
	if err := ValidateOAuthConfig(fullValid); err != nil {
		t.Fatalf("baseline: full valid cfg should pass, got %v", err)
	}
	fields := []struct {
		name   string
		mutate func(c *credentialacq.OAuthClientConfig)
	}{
		{"auth_url", func(c *credentialacq.OAuthClientConfig) { c.AuthURL = "" }},
		{"token_url", func(c *credentialacq.OAuthClientConfig) { c.TokenURL = "" }},
		{"client_id", func(c *credentialacq.OAuthClientConfig) { c.ClientID = "" }},
		{"redirect_uri", func(c *credentialacq.OAuthClientConfig) { c.RedirectURI = "" }},
	}
	for _, f := range fields {
		f := f
		t.Run(f.name, func(t *testing.T) {
			cfg := fullValid
			f.mutate(&cfg)
			if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrCursorOAuthConfigRequired) {
				t.Fatalf("missing %s: err=%v, want ErrCursorOAuthConfigRequired", f.name, err)
			}
		})
	}
}

func TestCursorOAuthAuthorizeURLUsesConfiguredPKCES256(t *testing.T) {
	// 消除的回归:已配置的 Cursor OAuth 值必须流入标准 PKCE authorize URL,
	// 而不依赖任何未经核实的默认值。
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:     "https://cursor-oauth.example.test/authorize",
		TokenURL:    "https://cursor-oauth.example.test/token",
		ClientID:    "cursor-client-id",
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
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://cursor-oauth.example.test/authorize" {
		t.Fatalf("authorize endpoint=%q", got)
	}
	q := parsed.Query()
	assertQueryValue(t, q, "response_type", "code")
	assertQueryValue(t, q, "client_id", "cursor-client-id")
	assertQueryValue(t, q, "redirect_uri", "http://127.0.0.1:1455/auth/callback")
	assertQueryValue(t, q, "state", "state-value")
	assertQueryValue(t, q, "code_challenge", "challenge-value")
	assertQueryValue(t, q, "code_challenge_method", CursorPKCEMethodS256)
	assertQueryValue(t, q, "scope", "openid offline_access")
}

func assertQueryValue(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("query %s=%q, want %q; all=%v", key, got, want, q)
	}
}
