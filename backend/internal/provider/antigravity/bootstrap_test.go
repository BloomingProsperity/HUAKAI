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
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		t.Fatalf("内置 OAuth client 不得为空：id=%q secret=%q", cfg.ClientID, cfg.ClientSecret)
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
	if cfg.AuthURL != credentialacq.AntigravityOAuthAuthURL {
		t.Fatalf("auth_url=%q，期望 %q", cfg.AuthURL, credentialacq.AntigravityOAuthAuthURL)
	}
	if err := ValidateOAuthConfig(cfg); err != nil {
		t.Fatalf("完整公开客户端配置应通过校验，err=%v", err)
	}
}

// TestAntigravityOAuthClientEnvOverride 守住环境变量 override 优先于内置默认。
func TestAntigravityOAuthClientEnvOverride(t *testing.T) {
	t.Setenv(AntigravityOAuthClientIDEnv, "operator-client-id")
	t.Setenv(AntigravityOAuthClientSecretEnv, "operator-client-secret")
	cfg := DefaultOAuthConfig()
	if cfg.ClientID != "operator-client-id" || cfg.ClientSecret != "operator-client-secret" {
		t.Fatalf("env override 未生效：id=%q secret=%q", cfg.ClientID, cfg.ClientSecret)
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
	approved := DefaultOAuthConfig()
	if cfg.TokenURL != AntigravityOAuthTokenEndpoint || cfg.ClientID != approved.ClientID || cfg.ClientSecret != approved.ClientSecret {
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
	assertAntigravityQueryValue(t, query, "client_id", approved.ClientID)
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
		if err := validateRefreshOAuthConfig(cfg); !errors.Is(err, ErrAntigravityOAuthConfigRequired) {
			t.Fatalf("被改写的公开 profile 应被拒绝，cfg=%+v err=%v", cfg, err)
		}
	}
}

func assertAntigravityQueryValue(t *testing.T, query url.Values, key, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("query %s=%q，期望 %q；all=%v", key, got, want, query)
	}
}
