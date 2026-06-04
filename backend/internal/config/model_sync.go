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
	Enabled         bool
	Interval        time.Duration
	Timeout         time.Duration
	AllowedHosts    []string
	AllowUnsafeURLs bool
	OpenAI          ModelSyncVendorConfig
	Anthropic       ModelSyncVendorConfig
	Gemini          ModelSyncVendorConfig
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
	allowUnsafeURLs, err := envBool("HUAKAI_MODEL_SYNC_UNSAFE_ALLOW_URLS")
	if err != nil {
		return nil, err
	}
	allowedHosts := splitModelSyncCSV(envTrim("HUAKAI_MODEL_SYNC_ALLOWED_HOSTS"))
	cfg := &ModelSyncConfig{
		Enabled:         enabled,
		Interval:        interval,
		Timeout:         timeout,
		AllowedHosts:    allowedHosts,
		AllowUnsafeURLs: allowUnsafeURLs,
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
	if err := validateModelSyncURLs(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c ModelSyncVendorConfig) Configured() bool {
	return strings.TrimSpace(c.APIKey) != ""
}

func validateModelSyncURLs(cfg *ModelSyncConfig) error {
	if cfg == nil {
		return nil
	}
	policy := modelsync.URLPolicy{
		AllowedHosts:   cfg.AllowedHosts,
		AllowUnsafeURL: cfg.AllowUnsafeURLs,
	}
	for _, item := range []struct {
		name   string
		vendor modelsync.Vendor
		cfg    ModelSyncVendorConfig
	}{
		{name: "openai", vendor: modelsync.VendorOpenAI, cfg: cfg.OpenAI},
		{name: "anthropic", vendor: modelsync.VendorAnthropic, cfg: cfg.Anthropic},
		{name: "gemini", vendor: modelsync.VendorGemini, cfg: cfg.Gemini},
	} {
		if !item.cfg.Configured() {
			continue
		}
		if err := modelsync.ValidateURL(modelsync.URLCheck{
			Vendor: item.vendor,
			RawURL: item.cfg.URL,
			Policy: policy,
		}); err != nil {
			return fmt.Errorf("HUAKAI_MODEL_SYNC_%s_URL rejected: %w", strings.ToUpper(item.name), err)
		}
	}
	return nil
}

func splitModelSyncCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
