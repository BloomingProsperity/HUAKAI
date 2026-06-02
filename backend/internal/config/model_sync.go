package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

type ModelSyncVendorConfig struct {
	APIKey string
	URL    string
}

type ModelSyncConfig struct {
	Enabled   bool
	Interval  time.Duration
	Timeout   time.Duration
	OpenAI    ModelSyncVendorConfig
	Anthropic ModelSyncVendorConfig
	Gemini    ModelSyncVendorConfig
}

func LoadModelSync() (*ModelSyncConfig, error) {
	enabled, err := envBool("HUAKAI_MODEL_SYNC_ENABLED")
	if err != nil {
		return nil, err
	}
	interval, err := envDurationSeconds("HUAKAI_MODEL_SYNC_INTERVAL_SECONDS", 3*time.Hour)
	if err != nil {
		return nil, err
	}
	timeout, err := envDurationSeconds("HUAKAI_MODEL_SYNC_TIMEOUT_SECONDS", 30*time.Second)
	if err != nil {
		return nil, err
	}
	cfg := &ModelSyncConfig{
		Enabled:  enabled,
		Interval: interval,
		Timeout:  timeout,
		OpenAI: ModelSyncVendorConfig{
			APIKey: envTrim("HUAKAI_MODEL_SYNC_OPENAI_API_KEY"),
			URL:    envDefault("HUAKAI_MODEL_SYNC_OPENAI_URL", modelsync.DefaultOpenAIModelsURL),
		},
		Anthropic: ModelSyncVendorConfig{
			APIKey: envTrim("HUAKAI_MODEL_SYNC_ANTHROPIC_API_KEY"),
			URL:    envDefault("HUAKAI_MODEL_SYNC_ANTHROPIC_URL", modelsync.DefaultAnthropicModelsURL),
		},
		Gemini: ModelSyncVendorConfig{
			APIKey: envTrim("HUAKAI_MODEL_SYNC_GEMINI_API_KEY"),
			URL:    envDefault("HUAKAI_MODEL_SYNC_GEMINI_URL", modelsync.DefaultGeminiModelsURL),
		},
	}
	if cfg.Enabled && !cfg.OpenAI.Configured() && !cfg.Anthropic.Configured() && !cfg.Gemini.Configured() {
		return nil, fmt.Errorf("HUAKAI_MODEL_SYNC_ENABLED=true requires at least one vendor API key")
	}
	return cfg, nil
}

func (c ModelSyncVendorConfig) Configured() bool {
	return strings.TrimSpace(c.APIKey) != ""
}
