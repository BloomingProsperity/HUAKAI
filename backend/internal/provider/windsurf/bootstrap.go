package windsurf

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

const (
	WindsurfVendor        = "windsurf"
	WindsurfAuthModeOAuth = "oauth"

	WindsurfPKCEMethodS256 = "S256"

	DefaultWindsurfOAuthRedirectURI = "http://127.0.0.1:1455/auth/callback"
)

var ErrWindsurfOAuthConfigRequired = errors.New("windsurf oauth: operator-verified OAuth config required")

func DefaultOAuthConfig() credentialacq.OAuthClientConfig {
	return credentialacq.OAuthClientConfig{
		RedirectURI: DefaultWindsurfOAuthRedirectURI,
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
	if override.Source != "" {
		cfg.Source = override.Source
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
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrWindsurfOAuthConfigRequired, strings.Join(missing, ","))
	}
	return nil
}

func BuildOAuthAuthorizeURL(cfg credentialacq.OAuthClientConfig, state, codeChallenge string) (string, error) {
	if err := ValidateOAuthConfig(cfg); err != nil {
		return "", err
	}
	raw := credentialacq.BuildAuthorizeURL(cfg, state, codeChallenge)
	if raw == "" {
		return "", fmt.Errorf("%w: authorize URL could not be built", ErrWindsurfOAuthConfigRequired)
	}
	return raw, nil
}
