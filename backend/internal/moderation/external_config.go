package moderation

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type PlatformSettingGetter interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type externalSettingsConfigStore struct {
	base     ConfigStore
	settings PlatformSettingGetter
}

func NewExternalSettingsConfigStore(base ConfigStore, settings PlatformSettingGetter) ConfigStore {
	return &externalSettingsConfigStore{base: base, settings: settings}
}

func (s *externalSettingsConfigStore) GetConfig(ctx context.Context, tenantID int64) (ModerationConfig, error) {
	cfg := DefaultConfig(tenantID)
	if s != nil && s.base != nil {
		var err error
		cfg, err = s.base.GetConfig(ctx, tenantID)
		if err != nil {
			return cfg, err
		}
	}
	if s == nil || s.settings == nil {
		return cfg, nil
	}
	external, err := ExternalModerationConfigFromSettings(ctx, s.settings)
	if err != nil {
		cfg.External = DefaultExternalModerationConfig()
		return cfg, nil
	}
	cfg.External = external
	return cfg, nil
}

func ExternalModerationConfigFromSettings(ctx context.Context, settings PlatformSettingGetter) (ExternalModerationConfig, error) {
	cfg := DefaultExternalModerationConfig()
	if settings == nil {
		return cfg, nil
	}
	var err error
	if cfg.Enabled, err = boolSetting(ctx, settings, platformsettings.KeyModerationExternalEnabled); err != nil {
		return cfg, err
	}
	if cfg.BaseURL, err = stringSetting(ctx, settings, platformsettings.KeyModerationExternalBaseURL); err != nil {
		return cfg, err
	}
	if cfg.APIKeys, err = stringSliceSetting(ctx, settings, platformsettings.KeyModerationExternalAPIKeys); err != nil {
		return cfg, err
	}
	if cfg.Model, err = stringSetting(ctx, settings, platformsettings.KeyModerationExternalModel); err != nil {
		return cfg, err
	}
	if cfg.Model == "" {
		cfg.Model = DefaultExternalModerationModel
	}
	if cfg.Thresholds, err = thresholdSetting(ctx, settings, platformsettings.KeyModerationExternalThresholds); err != nil {
		return cfg, err
	}
	if cfg.TimeoutMS, err = intSetting(ctx, settings, platformsettings.KeyModerationExternalTimeoutMS); err != nil {
		return cfg, err
	}
	if cfg.RetryCount, err = intSetting(ctx, settings, platformsettings.KeyModerationExternalRetryCount); err != nil {
		return cfg, err
	}
	if cfg.ImageEnabled, err = boolSetting(ctx, settings, platformsettings.KeyModerationExternalImageEnabled); err != nil {
		return cfg, err
	}
	return normalizeExternalModerationRuntimeConfig(cfg), nil
}

func stringSetting(ctx context.Context, settings PlatformSettingGetter, key platformsettings.SettingKey) (string, error) {
	stored, err := settings.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return stored.Value, nil
}

func boolSetting(ctx context.Context, settings PlatformSettingGetter, key platformsettings.SettingKey) (bool, error) {
	value, err := stringSetting(ctx, settings, key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(value)
}

func intSetting(ctx context.Context, settings PlatformSettingGetter, key platformsettings.SettingKey) (int, error) {
	value, err := stringSetting(ctx, settings, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

func stringSliceSetting(ctx context.Context, settings PlatformSettingGetter, key platformsettings.SettingKey) ([]string, error) {
	value, err := stringSetting(ctx, settings, key)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func thresholdSetting(ctx context.Context, settings PlatformSettingGetter, key platformsettings.SettingKey) (map[string]float64, error) {
	value, err := stringSetting(ctx, settings, key)
	if err != nil {
		return nil, err
	}
	var out map[string]float64
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, err
	}
	return out, nil
}
