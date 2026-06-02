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
	t.Setenv("HUAKAI_MODEL_SYNC_INTERVAL_SECONDS", "120")
	t.Setenv("HUAKAI_MODEL_SYNC_TIMEOUT_SECONDS", "7")
	t.Setenv("HUAKAI_MODEL_SYNC_OPENAI_URL", "https://openai.example/models")
	t.Setenv("HUAKAI_MODEL_SYNC_ANTHROPIC_URL", "https://anthropic.example/models")
	t.Setenv("HUAKAI_MODEL_SYNC_GEMINI_URL", "https://gemini.example/models")

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
		"HUAKAI_MODEL_SYNC_OPENAI_URL",
		"HUAKAI_MODEL_SYNC_ANTHROPIC_URL",
		"HUAKAI_MODEL_SYNC_GEMINI_URL",
	} {
		t.Setenv(key, "")
	}
}
