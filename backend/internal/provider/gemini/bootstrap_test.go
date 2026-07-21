package gemini

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestGeminiOAuthConfigRequiresOperatorVerifiedPKCEConfig(t *testing.T) {
	// 锁定回归：在 operator 提供 endpoint、client ID 和 scope 之前，Gemini OAuth
	// 必须 fail closed。变异自检：加入猜测的默认值会让本测试失败。
	cfg := DefaultOAuthConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" {
		t.Fatalf("default Gemini OAuth cfg must be operator-gated, got auth=%q token=%q client=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default Gemini OAuth scopes=%v, want none until operator config", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrGeminiOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrGeminiOAuthConfigRequired", err)
	}
}

func TestGeminiOAuthAuthorizeURLUsesOperatorPKCEValues(t *testing.T) {
	// 锁定回归：PKCE 授权 URL 生成必须防御性地拷贝 operator 配置，且不得依赖
	// 内置的 Gemini endpoint 或 scope。
	override := credentialacq.OAuthClientConfig{
		AuthURL:     "https://operator.google.example.test/oauth/authorize",
		TokenURL:    "https://operator.google.example.test/oauth/token",
		ClientID:    "gemini-client-id",
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
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://operator.google.example.test/oauth/authorize" {
		t.Fatalf("authorize endpoint=%q", got)
	}
	q := parsed.Query()
	assertGeminiQueryValue(t, q, "response_type", "code")
	assertGeminiQueryValue(t, q, "client_id", "gemini-client-id")
	assertGeminiQueryValue(t, q, "redirect_uri", "http://127.0.0.1:1455/auth/callback")
	assertGeminiQueryValue(t, q, "state", "state-value")
	assertGeminiQueryValue(t, q, "code_challenge", "challenge-value")
	assertGeminiQueryValue(t, q, "code_challenge_method", GeminiPKCEMethodS256)
	assertGeminiQueryValue(t, q, "scope", "openid email offline_access")
}

func TestGeminiOAuthConfigRejectsNonOperatorSource(t *testing.T) {
	// 锁定回归：source=operator_config 是一个强制校验点，而非展示用的元数据。
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:     "https://operator.google.example.test/oauth/authorize",
		TokenURL:    "https://operator.google.example.test/oauth/token",
		ClientID:    "gemini-client-id",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes:      []string{"openid", "offline_access"},
		Source:      credentialacq.ClientSourcePublicCLI,
	})
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("OAuthConfig source=%q, want forced operator_config", cfg.Source)
	}

	raw := cfg
	raw.Source = credentialacq.ClientSourcePublicCLI
	if err := ValidateOAuthConfig(raw); !errors.Is(err, ErrGeminiOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrGeminiOAuthConfigRequired", err)
	}
}

func assertGeminiQueryValue(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("query %s=%q, want %q; all=%v", key, got, want, q)
	}
}
