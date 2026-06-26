package openai_codex

import (
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

func TestOpenAICodexOAuthConfigRequiresOperatorDeviceCodeConfig(t *testing.T) {
	// 防回归：OpenAI Codex 的 device-code OAuth 绝不能内置猜测出来的
	// endpoint、client ID 或 scope。这些值的唯一权威来源是运维方配置。
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
	// 防回归：device-code 起始地址、token endpoint、client ID 与 scope 都必须
	// 由可信的运维方配置提供，并做防御性拷贝。
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
	// 防回归：source=operator_config 是一个强制校验点，而非展示用的元数据。
	// 变异自检：此处若接受 public_cli_client，会让调用方控制的配置冒充可信的
	// 运维方配置。
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
