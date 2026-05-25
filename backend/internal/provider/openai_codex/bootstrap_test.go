package openai_codex

import (
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestOpenAICodexOAuthConfigRequiresOperatorDeviceCodeConfig(t *testing.T) {
	// Regression killed: OpenAI Codex device-code OAuth must not ship guessed
	// endpoints, client IDs, or scopes. Operator config is the only source of
	// authority for those values.
	cfg := DefaultOAuthConfig()

	if cfg.AuthURL != "" || cfg.TokenURL != "" || cfg.ClientID != "" {
		t.Fatalf("default OpenAI Codex cfg must be operator-gated, got auth=%q token=%q client=%q", cfg.AuthURL, cfg.TokenURL, cfg.ClientID)
	}
	if len(cfg.Scopes) != 0 {
		t.Fatalf("default OpenAI Codex scopes=%v, want none until operator config", cfg.Scopes)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
	if err := ValidateOAuthConfig(cfg); !errors.Is(err, ErrOpenAICodexOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrOpenAICodexOAuthConfigRequired", err)
	}
}

func TestOpenAICodexOAuthConfigCopiesOperatorDeviceCodeValues(t *testing.T) {
	// Regression killed: device-code start, token endpoint, client ID, and
	// scope must be supplied by trusted operator config and copied defensively.
	override := credentialacq.OAuthClientConfig{
		AuthURL:  "https://auth.openai.example.test/oauth/device/code",
		TokenURL: "https://auth.openai.example.test/oauth/token",
		ClientID: "codex-client-id",
		Scopes:   []string{"openid", "offline_access", "profile"},
	}
	cfg := OAuthConfig(override)
	override.Scopes[0] = "mutated"

	if err := ValidateOAuthConfig(cfg); err != nil {
		t.Fatalf("ValidateOAuthConfig: %v", err)
	}
	if cfg.AuthURL != "https://auth.openai.example.test/oauth/device/code" || cfg.TokenURL != "https://auth.openai.example.test/oauth/token" {
		t.Fatalf("endpoints=(%q,%q), want operator endpoints", cfg.AuthURL, cfg.TokenURL)
	}
	if cfg.ClientID != "codex-client-id" {
		t.Fatalf("client_id=%q, want operator client id", cfg.ClientID)
	}
	if got := strings.Join(cfg.Scopes, " "); got != "openid offline_access profile" {
		t.Fatalf("scope=%q, want operator scopes", got)
	}
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("source=%q, want operator_config", cfg.Source)
	}
}

func TestOpenAICodexOAuthConfigRejectsNonOperatorSource(t *testing.T) {
	// Regression killed: source=operator_config is an enforcement point, not
	// display metadata. Mutation self-check: accepting public_cli_client here
	// lets caller-controlled config masquerade as trusted operator config.
	cfg := OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:  "https://auth.openai.example.test/oauth/device/code",
		TokenURL: "https://auth.openai.example.test/oauth/token",
		ClientID: "codex-client-id",
		Scopes:   []string{"openid", "offline_access"},
		Source:   credentialacq.ClientSourcePublicCLI,
	})
	if cfg.Source != credentialacq.ClientSourceOperatorConfig {
		t.Fatalf("OAuthConfig source=%q, want forced operator_config", cfg.Source)
	}

	raw := cfg
	raw.Source = credentialacq.ClientSourcePublicCLI
	if err := ValidateOAuthConfig(raw); !errors.Is(err, ErrOpenAICodexOAuthConfigRequired) {
		t.Fatalf("ValidateOAuthConfig err=%v, want ErrOpenAICodexOAuthConfigRequired", err)
	}
}
