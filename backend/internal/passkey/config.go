package passkey

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type PlatformSettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type PlatformSettingsConfigSource struct {
	Settings PlatformSettings
}

func NewPlatformSettingsConfigSource(settings PlatformSettings) PlatformSettingsConfigSource {
	return PlatformSettingsConfigSource{Settings: settings}
}

func (s PlatformSettingsConfigSource) Config(ctx context.Context) (Config, error) {
	if s.Settings == nil {
		return Config{}, ErrConfigNotConfigured
	}
	enabled, err := boolSetting(ctx, s.Settings, platformsettings.KeyPasskeyEnabled)
	if err != nil {
		return Config{}, err
	}
	registration, err := boolSetting(ctx, s.Settings, platformsettings.KeyPasskeyRegistrationEnabled)
	if err != nil {
		return Config{}, err
	}
	rpid, err := stringSetting(ctx, s.Settings, platformsettings.KeyPasskeyRPID)
	if err != nil {
		return Config{}, err
	}
	display, err := stringSetting(ctx, s.Settings, platformsettings.KeyPasskeyRPDisplayName)
	if err != nil {
		return Config{}, err
	}
	originsRaw, err := stringSetting(ctx, s.Settings, platformsettings.KeyPasskeyRPOrigins)
	if err != nil {
		return Config{}, err
	}
	origins, err := parseOrigins(originsRaw)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Enabled:             enabled,
		RegistrationEnabled: registration,
		RPID:                strings.TrimSpace(rpid),
		RPDisplayName:       firstNonEmpty(display, "HUAKAI"),
		RPOrigins:           origins,
		ChallengeTTL:        DefaultChallengeTTL,
	}, nil
}

func boolSetting(ctx context.Context, settings PlatformSettings, key platformsettings.SettingKey) (bool, error) {
	value, err := stringSetting(ctx, settings, key)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func stringSetting(ctx context.Context, settings PlatformSettings, key platformsettings.SettingKey) (string, error) {
	setting, err := settings.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(setting.Value), nil
}

func parseOrigins(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var origins []string
	if err := json.Unmarshal([]byte(raw), &origins); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			out = append(out, origin)
		}
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
