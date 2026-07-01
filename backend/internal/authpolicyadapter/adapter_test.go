package authpolicyadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestRegPolicyAdapterReadsSettings(t *testing.T) {
	ctx := context.Background()
	settings := stubSettings{
		values: map[platformsettings.SettingKey]string{
			platformsettings.KeyPasswordRegisterEnabled:      "false",
			platformsettings.KeyPasswordLoginEnabled:         "false",
			platformsettings.KeyEmailDomainAllowlistEnabled:  "true",
			platformsettings.KeyEmailDomainAllowlist:         "example.com",
			platformsettings.KeyEmailAliasRestrictionEnabled: "true",
			platformsettings.KeyReservedEmailLocalparts:      "admin,root",
		},
	}

	gate := NewRegistrationGate(settings)
	registerAllowed, err := gate.PasswordRegistrationAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed err=%v want nil", err)
	}
	if registerAllowed {
		t.Fatalf("PasswordRegistrationAllowed=true want false; MUTATION: adapter ignored password_register_enabled=false")
	}
	loginAllowed, err := gate.PasswordLoginAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed err=%v want nil", err)
	}
	if loginAllowed {
		t.Fatalf("PasswordLoginAllowed=true want false; MUTATION: adapter ignored password_login_enabled=false")
	}

	emailPolicy := NewEmailPolicy(settings)
	domainEnabled, domainList, err := emailPolicy.EmailDomainAllowlist(ctx, 42)
	if err != nil {
		t.Fatalf("EmailDomainAllowlist err=%v want nil", err)
	}
	if !domainEnabled || domainList != "example.com" {
		t.Fatalf("EmailDomainAllowlist=(%v,%q) want (true,%q); MUTATION: adapter ignored allowlist settings", domainEnabled, domainList, "example.com")
	}
	aliasEnabled, err := emailPolicy.EmailAliasRestrictionEnabled(ctx, 42)
	if err != nil {
		t.Fatalf("EmailAliasRestrictionEnabled err=%v want nil", err)
	}
	if !aliasEnabled {
		t.Fatalf("EmailAliasRestrictionEnabled=false want true; MUTATION: adapter ignored email_alias_restriction_enabled=true")
	}
	reserved, err := emailPolicy.ReservedEmailLocalparts(ctx, 42)
	if err != nil {
		t.Fatalf("ReservedEmailLocalparts err=%v want nil", err)
	}
	if reserved != "admin,root" {
		t.Fatalf("ReservedEmailLocalparts=%q want %q; MUTATION: adapter ignored reserved_email_localparts", reserved, "admin,root")
	}
}

// TestRegPolicyFailsToDefaultsOnReadError:设置读失败时,适配器回落到各 key 的**默认值**
//(AuthSettings.Get 吞错返回 defaultSetting),而非一律 fail-open。因此:
//   - 注册主门 registration_enabled 默认 false(Owner 拍板「注册默认关」)→ 读错时注册被拒(fail-safe);
//   - 密码登录 password_login_enabled 默认 true → 读错时登录仍放行;
//   - 邮箱域名白名单/别名限制默认关 → 读错时不误开限制。
// 变异:若适配器改成"读错一律返回 true/开",注册断言(期望 false)会 RED;若改成"读错一律 false",
// 登录断言(期望 true)会 RED——双向判别。
func TestRegPolicyFailsToDefaultsOnReadError(t *testing.T) {
	ctx := context.Background()
	settings := stubSettings{err: errors.New("settings unavailable")}

	gate := NewRegistrationGate(settings)
	registerAllowed, err := gate.PasswordRegistrationAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed err=%v want nil(回落默认)", err)
	}
	if registerAllowed {
		t.Fatalf("PasswordRegistrationAllowed=true want false;注册默认关,读错须回落到拒绝(fail-safe)")
	}
	loginAllowed, err := gate.PasswordLoginAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed err=%v want nil(回落默认)", err)
	}
	if !loginAllowed {
		t.Fatalf("PasswordLoginAllowed=false want true;登录默认开,读错须回落到放行")
	}

	emailPolicy := NewEmailPolicy(settings)
	domainEnabled, domainList, err := emailPolicy.EmailDomainAllowlist(ctx, 42)
	if err != nil {
		t.Fatalf("EmailDomainAllowlist err=%v want nil fail-open", err)
	}
	if domainEnabled || domainList != "" {
		t.Fatalf("EmailDomainAllowlist=(%v,%q) want (false,\"\") fail-open; MUTATION: read error enabled allowlist", domainEnabled, domainList)
	}
	aliasEnabled, err := emailPolicy.EmailAliasRestrictionEnabled(ctx, 42)
	if err != nil {
		t.Fatalf("EmailAliasRestrictionEnabled err=%v want nil fail-open", err)
	}
	if aliasEnabled {
		t.Fatalf("EmailAliasRestrictionEnabled=true want false fail-open; MUTATION: read error enabled alias restriction")
	}
	reserved, err := emailPolicy.ReservedEmailLocalparts(ctx, 42)
	if err != nil {
		t.Fatalf("ReservedEmailLocalparts err=%v want nil fail-open", err)
	}
	if reserved != "" {
		t.Fatalf("ReservedEmailLocalparts=%q want empty fail-open; MUTATION: read error enabled reserved local-parts", reserved)
	}
}

type stubSettings struct {
	values map[platformsettings.SettingKey]string
	err    error
}

func (s stubSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	if value, ok := s.values[key]; ok {
		return platformsettings.StoredSetting{Key: key, Value: value}, nil
	}
	value, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Key: key, Value: value, Source: platformsettings.SourceDefault}, nil
}
