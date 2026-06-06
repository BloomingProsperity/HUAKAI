package mediatask

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type SettingsReader interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type PlatformConfigSource struct {
	settings             SettingsReader
	billingPolicyVersion string
	requestClass         string
}

func NewPlatformConfigSource(settings SettingsReader, billingPolicyVersion, requestClass string) *PlatformConfigSource {
	return &PlatformConfigSource{
		settings:             settings,
		billingPolicyVersion: strings.TrimSpace(billingPolicyVersion),
		requestClass:         strings.TrimSpace(requestClass),
	}
}

func (s *PlatformConfigSource) Load(ctx context.Context) (Config, error) {
	if s == nil || s.settings == nil {
		return Config{}, ErrDisabled
	}
	enabled, err := s.stringSetting(ctx, platformsettings.KeyMediaTaskEnabled)
	if err != nil {
		return Config{}, err
	}
	baseURL, err := s.stringSetting(ctx, platformsettings.KeyMediaTaskProviderBaseURL)
	if err != nil {
		return Config{}, err
	}
	pollSecs, err := s.intSetting(ctx, platformsettings.KeyMediaTaskPollIntervalSecs)
	if err != nil {
		return Config{}, err
	}
	timeoutSecs, err := s.intSetting(ctx, platformsettings.KeyMediaTaskTimeoutSecs)
	if err != nil {
		return Config{}, err
	}
	defaultsRaw, err := s.stringSetting(ctx, platformsettings.KeyMediaTaskDefaultEstimatedCents)
	if err != nil {
		return Config{}, err
	}
	defaults, err := parseDefaultEstimatedCents(defaultsRaw)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Enabled: strings.TrimSpace(enabled) == "true", ProviderBaseURL: strings.TrimSpace(baseURL),
		PollInterval: time.Duration(pollSecs) * time.Second, TaskTimeout: time.Duration(timeoutSecs) * time.Second,
		DefaultEstimatedCents: defaults, BillingPolicyVersion: s.billingPolicyVersion, RequestClass: s.requestClass,
	}.withDefaults(), nil
}

func (s *PlatformConfigSource) stringSetting(ctx context.Context, key platformsettings.SettingKey) (string, error) {
	setting, err := s.settings.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *PlatformConfigSource) intSetting(ctx context.Context, key platformsettings.SettingKey) (int, error) {
	value, err := s.stringSetting(ctx, key)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%w: %s", ErrInvalidInput, key)
	}
	return parsed, nil
}

func parseDefaultEstimatedCents(raw string) (map[string]int64, error) {
	var out map[string]int64
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("%w: default estimated cents", ErrInvalidInput)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: default estimated cents", ErrInvalidInput)
	}
	for taskType, cents := range out {
		if strings.TrimSpace(taskType) == "" || cents < 0 {
			return nil, fmt.Errorf("%w: default estimated cents", ErrInvalidInput)
		}
	}
	return out, nil
}
