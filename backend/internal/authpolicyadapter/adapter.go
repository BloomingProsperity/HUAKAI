package authpolicyadapter

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/authpolicy"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type SettingsReader interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type AuthSettings struct {
	settings SettingsReader
}

func NewAuthSettings(settings SettingsReader) AuthSettings {
	return AuthSettings{settings: settings}
}

func NewRegistrationGate(settings SettingsReader, publicRegistrationTenantID int64) authpolicy.Policy {
	return authpolicy.New(NewAuthSettings(settings), publicRegistrationTenantID)
}

func (s AuthSettings) Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	setting, ok := readSetting(ctx, s.settings, key)
	if !ok {
		return defaultSetting(key), nil
	}
	return setting, nil
}

type EmailPolicy struct {
	settings SettingsReader
}

func NewEmailPolicy(settings SettingsReader) EmailPolicy {
	return EmailPolicy{settings: settings}
}

func (p EmailPolicy) EmailDomainAllowlist(ctx context.Context, tenantID int64) (bool, string, error) {
	_ = tenantID
	enabled, ok := p.boolSetting(ctx, platformsettings.KeyEmailDomainAllowlistEnabled, false)
	if !ok || !enabled {
		return false, "", nil
	}
	list, ok := p.stringSetting(ctx, platformsettings.KeyEmailDomainAllowlist, "")
	if !ok {
		return false, "", nil
	}
	return true, list, nil
}

func (p EmailPolicy) EmailAliasRestrictionEnabled(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	enabled, _ := p.boolSetting(ctx, platformsettings.KeyEmailAliasRestrictionEnabled, false)
	return enabled, nil
}

func (p EmailPolicy) ReservedEmailLocalparts(ctx context.Context, tenantID int64) (string, error) {
	_ = tenantID
	value, _ := p.stringSetting(ctx, platformsettings.KeyReservedEmailLocalparts, "")
	return value, nil
}

func (p EmailPolicy) boolSetting(ctx context.Context, key platformsettings.SettingKey, fallback bool) (bool, bool) {
	setting, ok := readSetting(ctx, p.settings, key)
	if !ok {
		return fallback, false
	}
	return setting.Value == "true", true
}

func (p EmailPolicy) stringSetting(ctx context.Context, key platformsettings.SettingKey, fallback string) (string, bool) {
	setting, ok := readSetting(ctx, p.settings, key)
	if !ok {
		return fallback, false
	}
	return setting.Value, true
}

func readSetting(ctx context.Context, settings SettingsReader, key platformsettings.SettingKey) (platformsettings.StoredSetting, bool) {
	if settings == nil {
		return platformsettings.StoredSetting{}, false
	}
	setting, err := settings.Get(ctx, key)
	if err != nil {
		return platformsettings.StoredSetting{}, false
	}
	return setting, true
}

func defaultSetting(key platformsettings.SettingKey) platformsettings.StoredSetting {
	value, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Scope: platformsettings.GlobalScope, Key: key, Value: value, Source: platformsettings.SourceDefault}
}
