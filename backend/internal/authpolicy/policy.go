package authpolicy

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type Policy struct {
	settings                   Settings
	publicRegistrationTenantID int64
}

func New(settings Settings, publicRegistrationTenantID int64) Policy {
	return Policy{settings: settings, publicRegistrationTenantID: publicRegistrationTenantID}
}

// PublicRegistrationTenantAllowed 限定匿名注册只能创建部署者工作租户的最终用户。
// 下级租户用户由该租户管理员在已认证的管理入口创建，不接受匿名请求自报租户。
func (p Policy) PublicRegistrationTenantAllowed(_ context.Context, tenantID int64) (bool, error) {
	return p.publicRegistrationTenantID > 0 && tenantID == p.publicRegistrationTenantID, nil
}

func (p Policy) PasswordRegistrationAllowed(ctx context.Context, tenantID int64) (bool, error) {
	_ = tenantID
	// 主开关 registration_enabled 与子开关 password_register_enabled 须同时为真才允许密码注册,
	// 与前端 loginEnhance(registrationEnabled && passwordRegisterEnabled)保持一致。
	// 原先只查子开关、漏了主开关，导致运营关闭「注册总开关」后仍可注册。
	master, err := p.boolSetting(ctx, platformsettings.KeyRegistrationEnabled)
	if err != nil || !master {
		return false, err
	}
	return p.boolSetting(ctx, platformsettings.KeyPasswordRegisterEnabled)
}

// RegistrationEnabled 是注册总开关(registration_enabled),驱动密码与社交两条注册路径的主门。
// 请求期读取后台设置，缺失或出错时 fail-closed，默认关闭注册。
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
