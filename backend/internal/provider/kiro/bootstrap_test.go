package kiro

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestKiroSSOConfigRequiresOperatorVerifiedEndpoints(t *testing.T) {
	// Regression killed: Kiro AWS SSO endpoint/client values must not be guessed.
	// Mutation self-check: hardcoding any default endpoint or client credential
	// makes this test fail before source/Owner-captured provenance exists.
	cfg := DefaultSSOConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" || cfg.ClientSecret != "" {
		t.Fatalf("default Kiro SSO cfg must be operator-gated, got auth=%q token=%q client=%q secret=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default Kiro SSO scopes=%v, want none until operator config", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateSSOConfig(cfg); !errors.Is(err, ErrKiroSSOConfigRequired) {
		t.Fatalf("ValidateSSOConfig err=%v, want ErrKiroSSOConfigRequired", err)
	}
}

func TestKiroSSOConfigUsesOperatorValues(t *testing.T) {
	cfg := SSOConfig(credentialacq.OAuthClientConfig{
		AuthURL:      "https://oidc.us-east-1.amazonaws.com/device_authorization",
		TokenURL:     "https://oidc.us-east-1.amazonaws.com/token",
		ClientID:     "operator-client-id",
		ClientSecret: "operator-client-secret",
		Scopes:       []string{"openid", "aws"},
	})

	if err := ValidateSSOConfig(cfg); err != nil {
		t.Fatalf("ValidateSSOConfig: %v", err)
	}
	if cfg.AuthURL != "https://oidc.us-east-1.amazonaws.com/device_authorization" {
		t.Fatalf("auth url=%q", cfg.AuthURL)
	}
	if cfg.TokenURL != "https://oidc.us-east-1.amazonaws.com/token" {
		t.Fatalf("token url=%q", cfg.TokenURL)
	}
	if cfg.ClientID != "operator-client-id" || cfg.ClientSecret != "operator-client-secret" {
		t.Fatalf("operator client fields not preserved: id=%q secret=%q", cfg.ClientID, cfg.ClientSecret)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "openid" || cfg.Scopes[1] != "aws" {
		t.Fatalf("scopes=%v, want [openid aws]", cfg.Scopes)
	}
}
