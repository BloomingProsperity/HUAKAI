package antigravity

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

const (
	AntigravityVendor        = "antigravity"
	AntigravityAuthModeOAuth = "oauth"

	AntigravityPKCEMethodS256 = "S256"

	DefaultAntigravityOAuthRedirectURI = credentialacq.AntigravityOAuthRedirectURI
	AntigravityOAuthTokenEndpoint      = credentialacq.AntigravityOAuthTokenURL
	AntigravityOAuthClientIDEnv        = credentialacq.AntigravityOAuthClientIDEnv
	AntigravityOAuthClientSecretEnv    = credentialacq.AntigravityOAuthClientSecretEnv
	antigravityOAuthScope              = credentialacq.AntigravityPublicCLIScope
)

// 以上 client_id/client_secret 是 Antigravity CLI 公开 native-app OAuth wire 值，
// 作为内置默认；运营者可经环境变量 override。

var ErrAntigravityOAuthConfigRequired = errors.New("antigravity oauth: 公开 OAuth 配置不完整或被改写")

// DefaultOAuthConfig 返回可直接用于 refresh_token grant 的固定公开客户端配置。
// 当前已确认的 wire 事实不包含授权页地址，因此 AuthURL 刻意留空；交互式授权仍由现有
// acquisition fail-closed 路径控制，导入后的 refresh 不受影响。
func DefaultOAuthConfig() credentialacq.OAuthClientConfig {
	return credentialacq.AntigravityPublicCLIConfig()
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
	approved := DefaultOAuthConfig()
	var mismatches []string
	if strings.TrimSpace(cfg.TokenURL) != AntigravityOAuthTokenEndpoint {
		mismatches = append(mismatches, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) != approved.ClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != approved.ClientSecret {
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

func scopeString(cfg credentialacq.OAuthClientConfig) string {
	scopes := make([]string, 0, len(cfg.Scopes))
	for _, scope := range cfg.Scopes {
		if trimmed := strings.TrimSpace(scope); trimmed != "" {
			scopes = append(scopes, trimmed)
		}
	}
	return strings.Join(scopes, " ")
}
