package antigravity

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

const (
	AntigravityVendor        = "antigravity"
	AntigravityAuthModeOAuth = "oauth"

	AntigravityPKCEMethodS256 = "S256"

	DefaultAntigravityOAuthRedirectURI = "http://127.0.0.1:1455/auth/callback"
	AntigravityOAuthTokenEndpoint      = "https://oauth2.googleapis.com/token"
	AntigravityOAuthClientIDEnv        = "HUAKAI_ANTIGRAVITY_OAUTH_CLIENT_ID"
	AntigravityOAuthClientSecretEnv    = "HUAKAI_ANTIGRAVITY_OAUTH_CLIENT_SECRET"
	antigravityBuiltinClientID         = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	antigravityBuiltinClientSecret     = "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"
	antigravityOAuthScope              = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"
)

// 以上 client_id/client_secret 是 Antigravity CLI 公开 native-app OAuth wire 值，
// 作为内置默认；运营者可经环境变量 override。

var ErrAntigravityOAuthConfigRequired = errors.New("antigravity oauth: 公开 OAuth 配置不完整或被改写")

// AntigravityOAuthClientID 返回 OAuth client_id：环境变量 override 优先，未设则回落内置默认。
func AntigravityOAuthClientID() string {
	if v := strings.TrimSpace(os.Getenv(AntigravityOAuthClientIDEnv)); v != "" {
		return v
	}
	return antigravityBuiltinClientID
}

// AntigravityOAuthClientSecret 返回 OAuth client_secret：环境变量 override 优先，未设则回落内置默认。
func AntigravityOAuthClientSecret() string {
	if v := strings.TrimSpace(os.Getenv(AntigravityOAuthClientSecretEnv)); v != "" {
		return v
	}
	return antigravityBuiltinClientSecret
}

// DefaultOAuthConfig 返回可直接用于 refresh_token grant 的固定公开客户端配置。
// 当前已确认的 wire 事实不包含授权页地址，因此 AuthURL 刻意留空；交互式授权仍由现有
// acquisition fail-closed 路径控制，导入后的 refresh 不受影响。
func DefaultOAuthConfig() credentialacq.OAuthClientConfig {
	return credentialacq.OAuthClientConfig{
		TokenURL:     AntigravityOAuthTokenEndpoint,
		ClientID:     AntigravityOAuthClientID(),
		ClientSecret: AntigravityOAuthClientSecret(),
		RedirectURI:  DefaultAntigravityOAuthRedirectURI,
		Scopes:       strings.Fields(antigravityOAuthScope),
		Source:       credentialacq.ClientSourcePublicCLI,
	}
}

// OAuthConfig 只接受授权页、回调地址和受控 HTTP client 覆盖；OAuth 客户端
// 身份始终按环境变量与内置默认解析，token 端点与 scopes 始终使用固定公开 profile。
func OAuthConfig(override credentialacq.OAuthClientConfig) credentialacq.OAuthClientConfig {
	cfg := DefaultOAuthConfig()
	if strings.TrimSpace(override.AuthURL) != "" {
		cfg.AuthURL = strings.TrimSpace(override.AuthURL)
	}
	if strings.TrimSpace(override.RedirectURI) != "" {
		cfg.RedirectURI = strings.TrimSpace(override.RedirectURI)
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	return cfg
}

// ValidateOAuthConfig 校验包含授权页的完整 PKCE profile。
func ValidateOAuthConfig(cfg credentialacq.OAuthClientConfig) error {
	if err := validateRefreshOAuthConfig(cfg); err != nil {
		return err
	}
	var missing []string
	if strings.TrimSpace(cfg.AuthURL) == "" {
		missing = append(missing, "auth_url")
	}
	if strings.TrimSpace(cfg.RedirectURI) == "" {
		missing = append(missing, "redirect_uri")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrAntigravityOAuthConfigRequired, strings.Join(missing, ","))
	}
	return nil
}

func validateRefreshOAuthConfig(cfg credentialacq.OAuthClientConfig) error {
	var mismatches []string
	if strings.TrimSpace(cfg.TokenURL) != AntigravityOAuthTokenEndpoint {
		mismatches = append(mismatches, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) != AntigravityOAuthClientID() {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != AntigravityOAuthClientSecret() {
		mismatches = append(mismatches, "client_secret")
	}
	if scopeString(cfg) != antigravityOAuthScope {
		mismatches = append(mismatches, "scope")
	}
	if strings.TrimSpace(cfg.Source) != credentialacq.ClientSourcePublicCLI {
		mismatches = append(mismatches, "source=public_cli_client")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: mismatch %s", ErrAntigravityOAuthConfigRequired, strings.Join(mismatches, ","))
	}
	return nil
}

func BuildOAuthAuthorizeURL(cfg credentialacq.OAuthClientConfig, state, codeChallenge string) (string, error) {
	if err := ValidateOAuthConfig(cfg); err != nil {
		return "", err
	}
	raw := credentialacq.BuildAuthorizeURL(cfg, state, codeChallenge)
	if raw == "" {
		return "", fmt.Errorf("%w: authorize URL could not be built", ErrAntigravityOAuthConfigRequired)
	}
	return raw, nil
}

func RefreshAdapterFromOAuthConfig(cfg credentialacq.OAuthClientConfig) (RefreshAdapter, error) {
	if err := validateRefreshOAuthConfig(cfg); err != nil {
		return RefreshAdapter{}, err
	}
	return RefreshAdapter{
		TokenURL:     AntigravityOAuthTokenEndpoint,
		ClientID:     AntigravityOAuthClientID(),
		ClientSecret: AntigravityOAuthClientSecret(),
		Scope:        antigravityOAuthScope,
		HTTPClient:   cfg.HTTPClient,
	}, nil
}

func scopeString(cfg credentialacq.OAuthClientConfig) string {
	scopes := make([]string, 0, len(cfg.Scopes))
	for _, scope := range cfg.Scopes {
		if trimmed := strings.TrimSpace(scope); trimmed != "" {
			scopes = append(scopes, trimmed)
		}
	}
	return strings.Join(scopes, " ")
}
