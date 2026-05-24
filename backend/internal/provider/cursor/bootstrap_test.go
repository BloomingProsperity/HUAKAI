package cursor

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestCursorOAuthConfigRequiresOperatorVerifiedEndpoints(t *testing.T) {
	// Regression killed: Cursor OAuth endpoints are not verified in current
	// allowed references. Mutation self-check: hardcoding a guessed endpoint,
	// client_id, or scope makes this test fail instead of silently shipping it.
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

func TestCursorOAuthAuthorizeURLUsesConfiguredPKCES256(t *testing.T) {
	// Regression killed: configured Cursor OAuth values must flow into the
	// standard PKCE authorize URL without relying on unverified defaults.
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
