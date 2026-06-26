package kiro

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestKiroSSOConfigRequiresOperatorVerifiedEndpoints(t *testing.T) {
	// 锁定回归：Kiro AWS SSO 的 endpoint/client 值不得靠猜测得来。变异自检：
	// 硬编码任何默认 endpoint 或 client 凭据，会让本测试在 source/Owner 采集的
	// 来源凭证存在之前就失败。
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
