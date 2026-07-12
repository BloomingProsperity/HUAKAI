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
	// 主开关 registration_enabled 与子开关 password_register_enabled 须同时为真才允许密码注册,
	// 与前端 loginEnhance(registrationEnabled && passwordRegisterEnabled)及 sub2api 的请求期主门
	// IsRegistrationEnabled 语义一致。原先只查子开关、漏了主开关 → 运营在后台关「注册总开关」实际不生效。
	master, err := p.boolSetting(ctx, platformsettings.KeyRegistrationEnabled)
	if err != nil || !master {
		return false, err
	}
	return p.boolSetting(ctx, platformsettings.KeyPasswordRegisterEnabled)
}

// RegistrationEnabled 是注册总开关(registration_enabled),驱动密码与社交两条注册路径的主门。
// 对齐 sub2api 的 IsRegistrationEnabled:请求期读后台设置,缺失/出错 fail-closed(默认关注册)。
func (p Policy) RegistrationEnabled(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	return p.boolSetting(ctx, platformsettings.KeyRegistrationEnabled)
}

// InvitationRequired 表示注册是否必须邀请码(invitation_required),由后台设置驱动。
func (p Policy) InvitationRequired(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	return p.boolSetting(ctx, platformsettings.KeyInvitationRequired)
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
