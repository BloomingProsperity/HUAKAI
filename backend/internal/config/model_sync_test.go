package config

import (
	"testing"
	"time"
)

func TestLoadModelSyncDefaultDisabled(t *testing.T) {
	clearModelSyncEnv(t)
	cfg, err := LoadModelSync()
	if err != nil {
		t.Fatalf("LoadModelSync: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("model sync should be disabled by default")
	}
	if cfg.Interval != 3*time.Hour || cfg.Timeout != 30*time.Second {
		t.Fatalf("defaults interval=%s timeout=%s", cfg.Interval, cfg.Timeout)
	}
}

func TestLoadModelSyncEnabledRequiresAtLeastOneVendorKey(t *testing.T) {
	clearModelSyncEnv(t)
	t.Setenv("HUAKAI_MODEL_SYNC_ENABLED", "true")

	_, err := LoadModelSync()
	if err == nil {
		t.Fatalf("LoadModelSync enabled without vendor keys returned nil error")
	}
}

func TestLoadModelSyncParsesKeysURLsAndDurations(t *testing.T) {
	clearModelSyncEnv(t)
	t.Setenv("HUAKAI_MODEL_SYNC_ENABLED", "true")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_API_KEY", "openai-key")
	t.Setenv("HUAKAI_MODEL_SYNC_ANTHROPIC_API_KEY", "anthropic-key")
	t.Setenv("HUAKAI_MODEL_SYNC_GEMINI_API_KEY", "gemini-key")
	t.Setenv("HUAKAI_MODEL_SYNC_GROK_API_KEY", "grok-key")
	t.Setenv("HUAKAI_MODEL_SYNC_INTERVAL_SECONDS", "120")
	t.Setenv("HUAKAI_MODEL_SYNC_TIMEOUT_SECONDS", "7")
	t.Setenv("HUAKAI_MODEL_SYNC_ALLOWED_HOSTS", "openai.example,anthropic.example,gemini.example,grok.example")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_URL", "https://openai.example/models")
	t.Setenv("HUAKAI_MODEL_SYNC_ANTHROPIC_URL", "https://anthropic.example/models")
	t.Setenv("HUAKAI_MODEL_SYNC_GEMINI_URL", "https://gemini.example/models")
	t.Setenv("HUAKAI_MODEL_SYNC_GROK_URL", "https://grok.example/models")

	cfg, err := LoadModelSync()
	if err != nil {
		t.Fatalf("LoadModelSync: %v", err)
	}
	if !cfg.Enabled || cfg.Interval != 120*time.Second || cfg.Timeout != 7*time.Second {
		t.Fatalf("parsed enabled/interval/timeout mismatch: %+v", cfg)
	}
	if cfg.OpenAI.APIKey != "openai-key" || cfg.OpenAI.URL != "https://openai.example/models" {
		t.Fatalf("OpenAI config mismatch: %+v", cfg.OpenAI)
	}
	if cfg.Anthropic.APIKey != "anthropic-key" || cfg.Anthropic.URL != "https://anthropic.example/models" {
		t.Fatalf("Anthropic config mismatch: %+v", cfg.Anthropic)
	}
	if cfg.Gemini.APIKey != "gemini-key" || cfg.Gemini.URL != "https://gemini.example/models" {
		t.Fatalf("Gemini config mismatch: %+v", cfg.Gemini)
	}
	if cfg.Grok.APIKey != "grok-key" || cfg.Grok.URL != "https://grok.example/models" {
		t.Fatalf("Grok config mismatch: %+v", cfg.Grok)
	}
	if len(cfg.AllowedHosts) != 4 || cfg.AllowUnsafeURLs {
		t.Fatalf("model-sync URL policy mismatch: hosts=%v unsafe=%v", cfg.AllowedHosts, cfg.AllowUnsafeURLs)
	}
}

func TestLoadModelSyncRejectsUnsafeURLWithoutOverride(t *testing.T) {
	// 根因:env URL 以前不做出站安全校验。Mutation:移除 model-sync URL policy 时，
	// 169.254 metadata URL 会被配置接受，后续 fetcher 可携带 key 出站。
	clearModelSyncEnv(t)
	t.Setenv("HUAKAI_MODEL_SYNC_ENABLED", "true")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_API_KEY", "openai-key")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_URL", "https://169.254.169.254/v1/models")

	if _, err := LoadModelSync(); err == nil {
		t.Fatalf("LoadModelSync accepted metadata model-sync URL without unsafe override")
	}
}

func TestLoadModelSyncUnsafeOverrideAllowsExplicitTestEndpoint(t *testing.T) {
	clearModelSyncEnv(t)
	t.Setenv("HUAKAI_MODEL_SYNC_ENABLED", "true")
	t.Setenv("HUAKAI_MODEL_SYNC_UNSAFE_ALLOW_URLS", "true")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_API_KEY", "openai-key")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_URL", "http://127.0.0.1:18080/v1/models")

	cfg, err := LoadModelSync()
	if err != nil {
		t.Fatalf("LoadModelSync with explicit unsafe override: %v", err)
	}
	if cfg.OpenAI.URL != "http://127.0.0.1:18080/v1/models" {
		t.Fatalf("OpenAI URL=%q want explicit test endpoint", cfg.OpenAI.URL)
	}
	if !cfg.AllowUnsafeURLs {
		t.Fatalf("AllowUnsafeURLs=false want true from explicit unsafe override")
	}
}

func clearModelSyncEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HUAKAI_MODEL_SYNC_ENABLED",
		"HUAKAI_MODEL_SYNC_INTERVAL_SECONDS",
		"HUAKAI_MODEL_SYNC_TIMEOUT_SECONDS",
		"HUAKAI_MODEL_SYNC_OPENAI_API_KEY",
		"HUAKAI_MODEL_SYNC_ANTHROPIC_API_KEY",
		"HUAKAI_MODEL_SYNC_GEMINI_API_KEY",
		"HUAKAI_MODEL_SYNC_GROK_API_KEY",
		"HUAKAI_MODEL_SYNC_OPENAI_URL",
		"HUAKAI_MODEL_SYNC_ANTHROPIC_URL",
		"HUAKAI_MODEL_SYNC_GEMINI_URL",
		"HUAKAI_MODEL_SYNC_GROK_URL",
		"HUAKAI_MODEL_SYNC_ALLOWED_HOSTS",
		"HUAKAI_MODEL_SYNC_UNSAFE_ALLOW_URLS",
	} {
		t.Setenv(key, "")
	}
}
