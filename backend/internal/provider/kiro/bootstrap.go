package kiro

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

const (
	KiroVendor         = "kiro"
	KiroAuthModeAWSSSO = "aws-sso"

	kiroAuthModeSSOAlias = "sso"
	kiroCredentialMode   = "kiro_session"
)

var ErrKiroSSOConfigRequired = errors.New("kiro sso: operator-verified AWS SSO config required")

func DefaultSSOConfig() credentialacq.OAuthClientConfig {
	return credentialacq.OAuthClientConfig{
		Source: credentialacq.ClientSourceOperatorConfig,
	}
}

func SSOConfig(override credentialacq.OAuthClientConfig) credentialacq.OAuthClientConfig {
	cfg := DefaultSSOConfig()
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

func ValidateSSOConfig(cfg credentialacq.OAuthClientConfig) error {
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
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		missing = append(missing, "client_secret")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrKiroSSOConfigRequired, strings.Join(missing, ","))
	}
	return nil
}
