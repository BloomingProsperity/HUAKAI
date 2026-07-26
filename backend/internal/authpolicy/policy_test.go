package authpolicy

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// TestPasswordPolicyDefaults 锁定「注册默认关」及前端
// registrationEnabled && passwordRegisterEnabled 的一致语义：默认设置下主开关 registration_enabled=false，
// 密码注册**默认不允许**(运营须在后台开总开关);但密码登录不受注册总开关约束,默认允许。
// 变异:PasswordRegistrationAllowed 去掉主开关 registration_enabled 检查 → 默认变 true,首断言 RED
// (这正是修复前「后端比前端宽松、直打 API 可绕过隐藏的注册入口」的口子)。
func TestPasswordPolicyDefaults(t *testing.T) {
	svc := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	policy := New(svc, 1)

	registerAllowed, err := policy.PasswordRegistrationAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed default: %v", err)
	}
	if registerAllowed {
		t.Fatal("注册总开关默认关,密码注册应默认不允许(得 true)")
	}
	loginAllowed, err := policy.PasswordLoginAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed default: %v", err)
	}
	if !loginAllowed {
		t.Fatal("密码登录不受注册总开关约束,默认应允许(得 false)")
	}
}

// TestPasswordPolicyMasterSwitch 锁定主开关 registration_enabled 真正当总闸:仅开子开关不够,须总开关也开。
func TestPasswordPolicyMasterSwitch(t *testing.T) {
	ctx := context.Background()
	svc := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	seed := func(k platformsettings.SettingKey, v string) {
		if _, err := svc.Upsert(ctx, platformsettings.UpsertInput{Key: k, Value: v, UpdatedBy: "test"}); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	policy := New(svc, 1)

	seed(platformsettings.KeyPasswordRegisterEnabled, "true") // 仅开子开关
	if allowed, _ := policy.PasswordRegistrationAllowed(ctx, 1); allowed {
		t.Fatal("总开关关时,即便密码注册子开关开也应拒绝")
	}
	seed(platformsettings.KeyRegistrationEnabled, "true") // 再开总开关
	if allowed, err := policy.PasswordRegistrationAllowed(ctx, 1); err != nil || !allowed {
		t.Fatalf("总开关+子开关均开应允许,得 allowed=%v err=%v", allowed, err)
	}
	seed(platformsettings.KeyPasswordRegisterEnabled, "false") // 关子开关
	if allowed, _ := policy.PasswordRegistrationAllowed(ctx, 1); allowed {
		t.Fatal("密码注册子开关关时应拒绝(子开关也是必要条件)")
	}
}

// TestRegistrationEnabledReader 锁定供社交注册门用的总开关读取器。
func TestRegistrationEnabledReader(t *testing.T) {
	ctx := context.Background()
	svc := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	policy := New(svc, 1)
	if enabled, _ := policy.RegistrationEnabled(ctx, 1); enabled {
		t.Fatal("RegistrationEnabled 默认应为 false(fail-closed)")
	}
	if _, err := svc.Upsert(ctx, platformsettings.UpsertInput{Key: platformsettings.KeyRegistrationEnabled, Value: "true", UpdatedBy: "t"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if enabled, err := policy.RegistrationEnabled(ctx, 1); err != nil || !enabled {
		t.Fatalf("开总开关后应 true,得 %v err=%v", enabled, err)
	}
}

func TestPasswordPolicyReadsPlatformSettings(t *testing.T) {
	store := platformsettings.NewMemoryStore()
	svc := platformsettings.NewService(store, nil)
	if _, err := svc.Upsert(context.Background(), platformsettings.UpsertInput{Key: platformsettings.KeyPasswordRegisterEnabled, Value: "false", UpdatedBy: "test"}); err != nil {
		t.Fatalf("seed password_register_enabled: %v", err)
	}
	if _, err := svc.Upsert(context.Background(), platformsettings.UpsertInput{Key: platformsettings.KeyPasswordLoginEnabled, Value: "false", UpdatedBy: "test"}); err != nil {
		t.Fatalf("seed password_login_enabled: %v", err)
	}
	policy := New(svc, 1)

	registerAllowed, err := policy.PasswordRegistrationAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed: %v", err)
	}
	if registerAllowed {
		t.Fatal("password registration allowed=true want false")
	}
	loginAllowed, err := policy.PasswordLoginAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed: %v", err)
	}
	if loginAllowed {
		t.Fatal("password login allowed=true want false")
	}
}
