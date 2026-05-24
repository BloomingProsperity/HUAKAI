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

	DefaultAntigravityOAuthRedirectURI = "http://127.0.0.1:1455/auth/callback"
)

var ErrAntigravityOAuthConfigRequired = errors.New("antigravity oauth: operator-verified OAuth config required")

func DefaultOAuthConfig() credentialacq.OAuthClientConfig {
	return credentialacq.OAuthClientConfig{
		RedirectURI: DefaultAntigravityOAuthRedirectURI,
		Source:      credentialacq.ClientSourceOperatorConfig,
	}
}

func OAuthConfig(override credentialacq.OAuthClientConfig) credentialacq.OAuthClientConfig {
	cfg := DefaultOAuthConfig()
	if override.AuthURL != "" {
		cfg.AuthURL = override.AuthURL
	}
	if override.TokenURL != "" {
		cfg.TokenURL = override.TokenURL
	}
	if override.ClientID != "" {
		cfg.ClientID = override.ClientID
	}
	if override.ClientSecret != "" {
		cfg.ClientSecret = override.ClientSecret
	}
	if override.RedirectURI != "" {
		cfg.RedirectURI = override.RedirectURI
	}
	if len(override.Scopes) > 0 {
		cfg.Scopes = append([]string(nil), override.Scopes...)
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	return cfg
}

func ValidateOAuthConfig(cfg credentialacq.OAuthClientConfig) error {
	var missing []string
	if strings.TrimSpace(cfg.AuthURL) == "" {
		missing = append(missing, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		missing = append(missing, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		missing = append(missing, "client_id")
	}
	if strings.TrimSpace(cfg.RedirectURI) == "" {
		missing = append(missing, "redirect_uri")
	}
	if scopeString(cfg) == "" {
		missing = append(missing, "scope")
	}
	if strings.TrimSpace(cfg.Source) != credentialacq.ClientSourceOperatorConfig {
		missing = append(missing, "source=operator_config")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrAntigravityOAuthConfigRequired, strings.Join(missing, ","))
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
	if err := ValidateOAuthConfig(cfg); err != nil {
		return RefreshAdapter{}, err
	}
	return RefreshAdapter{
		TokenURL:     strings.TrimSpace(cfg.TokenURL),
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scope:        scopeString(cfg),
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
