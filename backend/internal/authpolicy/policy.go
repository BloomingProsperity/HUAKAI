package authpolicy

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type Policy struct {
	settings Settings
}

func New(settings Settings) Policy {
	return Policy{settings: settings}
}

func (p Policy) PasswordRegistrationAllowed(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	return p.boolSetting(ctx, platformsettings.KeyPasswordRegisterEnabled)
}

func (p Policy) PasswordLoginAllowed(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	return p.boolSetting(ctx, platformsettings.KeyPasswordLoginEnabled)
}

func (p Policy) boolSetting(ctx context.Context, key platformsettings.SettingKey) (bool, error) {
	if p.settings == nil {
		return true, nil
	}
	setting, err := p.settings.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return setting.Value == "true", nil
}
