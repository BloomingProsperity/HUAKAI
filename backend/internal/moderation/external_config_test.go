package moderation

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestExternalSettingsConfigStoreMergesPlatformSettings(t *testing.T) {
	// Mutation: wiring the screener to the SQL moderation config directly,
	// without merging platformsettings, leaves External disabled and this red.
	settings := &externalSettingsStub{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyModerationExternalEnabled:      "true",
		platformsettings.KeyModerationExternalBaseURL:      "https://moderation.example.test/v1/moderations",
		platformsettings.KeyModerationExternalAPIKeys:      `["key-a","key-b"]`,
		platformsettings.KeyModerationExternalModel:        "omni-moderation-latest",
		platformsettings.KeyModerationExternalThresholds:   `{"violence":0.73}`,
		platformsettings.KeyModerationExternalTimeoutMS:    "2500",
		platformsettings.KeyModerationExternalRetryCount:   "4",
		platformsettings.KeyModerationExternalImageEnabled: "true",
	}}
	store := NewExternalSettingsConfigStore(configStub{cfg: ModerationConfig{
		TenantID: 7, Enabled: true, FailClosed: true,
	}}, settings)

	cfg, err := store.GetConfig(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if !cfg.Enabled || !cfg.External.Enabled || !cfg.External.ImageEnabled {
		t.Fatalf("config did not preserve base enabled or external flags: %+v", cfg)
	}
	if cfg.External.BaseURL != "https://moderation.example.test/v1/moderations" ||
		cfg.External.Model != "omni-moderation-latest" ||
		cfg.External.TimeoutMS != 2500 ||
		cfg.External.RetryCount != 4 {
		t.Fatalf("external config mismatch: %+v", cfg.External)
	}
	if len(cfg.External.APIKeys) != 2 || cfg.External.APIKeys[0] != "key-a" || cfg.External.APIKeys[1] != "key-b" {
		t.Fatalf("api keys mismatch: %+v", cfg.External.APIKeys)
	}
	if cfg.External.Thresholds["violence"] != 0.73 {
		t.Fatalf("thresholds mismatch: %+v", cfg.External.Thresholds)
	}
}

type externalSettingsStub struct {
	values map[platformsettings.SettingKey]string
}

func (s *externalSettingsStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	value, ok := s.values[key]
	if !ok {
		value, _ = platformsettings.DefaultValue(key)
	}
	return platformsettings.StoredSetting{Key: key, Value: value, Source: platformsettings.SourceDB}, nil
}
