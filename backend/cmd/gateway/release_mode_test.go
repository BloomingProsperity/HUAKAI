package main

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// TestValidateReleaseMode_RequiresExplicitMode 守护:网关不得在 HUAKAI_RELEASE_MODE
// 缺失或无法识别的情况下启动,因为那会悄悄选中 dev 路径——临时密钥、内存账本、
// 跳过发布门控。
//
// 变异检查:在 validateReleaseMode 里重新放行空字符串,下方的 missing_env 用例即失败,
// 而显式的 dev/test 模式仍证明本地与 CI 运行有受支持的非 production 路径。
func TestValidateReleaseMode_RequiresExplicitMode(t *testing.T) {
	allowed := []string{"dev", "development", "DEV", "test", "production", "Production", " production "}
	for _, v := range allowed {
		v := v
		t.Run("allow/"+v, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", v)
			if err := validateReleaseMode(); err != nil {
				t.Fatalf("validateReleaseMode(%q) = %v, want nil (recognized mode must boot)", v, err)
			}
		})
	}

	rejected := []string{"", " ", "prod", "PROD", "produciton", "prd", "prod-us", "release", "staging", "1"}
	for _, v := range rejected {
		v := v
		t.Run("reject/"+v, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", v)
			err := validateReleaseMode()
			if err == nil {
				t.Fatalf("validateReleaseMode(%q) = nil, want fail-closed error (silent dev degradation)", v)
			}
			if strings.TrimSpace(v) != "" && !strings.Contains(err.Error(), v) {
				t.Fatalf("error %q should echo the offending value %q", err.Error(), v)
			}
		})
	}
}

// TestValidateReleaseMode_TypoDoesNotEnableProduction 证明那个*后果*:
// 拼错的 production 取值既会被 validateReleaseMode 拒绝,也不会把 releaseModeProduction()
// 翻成 true——也就是说,它确实是该门控如今所封堵的那条悄无声息的
// dev 降级路径。
func TestValidateReleaseMode_TypoDoesNotEnableProduction(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "prod")
	if releaseModeProduction() {
		t.Fatal("guard precondition broken: \"prod\" should not satisfy releaseModeProduction()")
	}
	if err := validateReleaseMode(); err == nil {
		t.Fatal("\"prod\" must be rejected at startup so it cannot silently run as dev")
	}
}

// TestValidateDevAuthTokenFlag 守护:HUAKAI_DEV_AUTH_RETURN_TOKEN=true 会把一次性的
// 验证/重置原始 secret 回显进公开的 register/reset JSON 响应里。在 production 下,它
// 必须在启动时 fail-closed,而不是仅打一条警告就启动(此前的行为)。
//
// 变异检查:删掉 validateDevAuthTokenFlag 里的 production+flag 守护(永远 return nil);
// 则 prod+flag 用例由绿转红。dev/无 flag 用例证明本地/CI 易用性仍可启动。
func TestValidateDevAuthTokenFlag(t *testing.T) {
	t.Run("prod+flag=fail", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
		if err := validateDevAuthTokenFlag(); err == nil {
			t.Fatal("production + HUAKAI_DEV_AUTH_RETURN_TOKEN=true must fail closed at startup")
		}
	})
	t.Run("prod+noflag=ok", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "")
		if err := validateDevAuthTokenFlag(); err != nil {
			t.Fatalf("production without the dev flag must boot; got %v", err)
		}
	})
	t.Run("dev+flag=ok", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "dev")
		t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
		if err := validateDevAuthTokenFlag(); err != nil {
			t.Fatalf("dev mode with the flag must still boot (CI/local ergonomics); got %v", err)
		}
	})
}

// TestProductionCaptchaConfigWarnsAndMarksHealthWhenSecretMissing 守护那项可用性
// 修正:缺失 Turnstile secret 不应让 production 启动失败,但它必须产生一条运维可见的
// WARN 以及降级的配置状态。
// 变异检查:恢复成 fail-boot、去掉 WARN,或把配置标成 OK;
// 本测试就在这种过度修复或静默错配回归处转红。
func TestProductionCaptchaConfigWarnsAndMarksHealthWhenSecretMissing(t *testing.T) {
	ctx := context.Background()
	enabledSettings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := enabledSettings.Upsert(ctx, platformsettings.UpsertInput{
		Key: platformsettings.KeyCaptchaEnabled, Value: "true", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("enable captcha setting: %v", err)
	}
	disabledSettings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := disabledSettings.Upsert(ctx, platformsettings.UpsertInput{
		Key: platformsettings.KeyCaptchaEnabled, Value: "false", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("disable captcha setting: %v", err)
	}

	t.Run("production_enabled_empty_secret_warns_and_boots", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		if err := validateProductionCaptchaConfig(ctx, enabledSettings, ""); err != nil {
			t.Fatalf("production captcha_enabled=true with empty Turnstile secret must boot with warning, got %v", err)
		}
		status, err := captchaConfigurationStatus(ctx, enabledSettings, "")
		if err != nil {
			t.Fatalf("captchaConfigurationStatus: %v", err)
		}
		if status.ConfigurationOK || status.SecretConfigured || status.Issue != "turnstile_secret_missing" {
			t.Fatalf("status=%+v want degraded missing-secret marker", status)
		}
		core, logs := observer.New(zapcore.WarnLevel)
		logCaptchaConfig(ctx, zap.New(core), enabledSettings, "")
		if logs.Len() != 1 {
			t.Fatalf("warn logs=%d want 1: %+v", logs.Len(), logs.All())
		}
		entry := logs.All()[0]
		fields := entry.ContextMap()
		if entry.Level != zapcore.WarnLevel || fields["captcha_configuration_issue"] != "turnstile_secret_missing" ||
			fields["captcha_configuration_ok"] != false || fields["turnstile_secret_configured"] != false {
			t.Fatalf("warn entry level=%s fields=%+v", entry.Level, fields)
		}
	})
	t.Run("production_enabled_secret_present_ok", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		if err := validateProductionCaptchaConfig(ctx, enabledSettings, "secret"); err != nil {
			t.Fatalf("production captcha with configured secret must boot: %v", err)
		}
		status, err := captchaConfigurationStatus(ctx, enabledSettings, "secret")
		if err != nil {
			t.Fatalf("captchaConfigurationStatus: %v", err)
		}
		if !status.ConfigurationOK || !status.SecretConfigured || status.Issue != "" {
			t.Fatalf("status=%+v want healthy configured captcha", status)
		}
	})
	t.Run("production_disabled_empty_secret_ok", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		if err := validateProductionCaptchaConfig(ctx, disabledSettings, ""); err != nil {
			t.Fatalf("production captcha disabled must keep missing-secret boot path: %v", err)
		}
	})
	t.Run("dev_enabled_empty_secret_ok", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "dev")
		if err := validateProductionCaptchaConfig(ctx, enabledSettings, ""); err != nil {
			t.Fatalf("dev missing-secret noop path must remain available: %v", err)
		}
	})
}

// TestLoadUserRegistrationModeFromEnv 守护:production 启动不得仅因没有运维显式配置注册策略,
// 就让公开注册保持开放。dev/test 保留历史上的开放默认值,而 production 默认禁用,直到运维
// 主动选择 open 或 invite-required 注册。
func TestLoadUserRegistrationModeFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		releaseMode string
		regMode     string
		want        userauth.RegistrationMode
		wantErr     bool
	}{
		{name: "production_unset_fails_closed", releaseMode: "production", want: userauth.RegistrationModeDisabled},
		{name: "dev_unset_stays_open", releaseMode: "", want: userauth.RegistrationModeOpen},
		{name: "production_explicit_open", releaseMode: "production", regMode: "open", want: userauth.RegistrationModeOpen},
		{name: "production_explicit_invite_required", releaseMode: "production", regMode: "invite_required", want: userauth.RegistrationModeInviteRequired},
		{name: "admin_only_alias_disables_public_registration", releaseMode: "production", regMode: "admin_only", want: userauth.RegistrationModeDisabled},
		{name: "misspelled_value_rejected", releaseMode: "production", regMode: "invite-required", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", tt.releaseMode)
			t.Setenv("HUAKAI_USER_REGISTRATION_MODE", tt.regMode)
			got, err := loadUserRegistrationModeFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loadUserRegistrationModeFromEnv() = nil error, want rejection for %q", tt.regMode)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadUserRegistrationModeFromEnv() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("loadUserRegistrationModeFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReleaseModeMissingErrorGuidesOperator 守护:HUAKAI_RELEASE_MODE 未设/拼错时,拒启错误必须
// 给出可照抄的取值指引(至少含 production 与 dev),否则运维只看到"required"却不知道填什么。
//
// 变异检查:把空值/未知值的报错改回不含示例的裸文案,本测试即 red。
func TestReleaseModeMissingErrorGuidesOperator(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "")
	err := validateReleaseMode()
	if err == nil {
		t.Fatal("空 HUAKAI_RELEASE_MODE 必须拒启")
	}
	for _, want := range []string{"production", "dev"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("空值拒启文案应含取值指引 %q,实际:%q", want, err.Error())
		}
	}
}

// TestProductionAuditKeyErrorGuidesOperator 守护:production 缺审计私钥拒启时,错误必须直接给出
// 生成命令与设置项(openssl / 脚本 / HUAKAI_AUDIT_PRIVATE_KEY_PATH),把运维从"知道缺却不知怎么补"里救出来。
//
// 变异检查:从拒启文案里抹掉 auditKeySetupHint(或清空该常量),本测试即 red。
func TestProductionAuditKeyErrorGuidesOperator(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	err := requireProductionChannelHealthSigner(nil)
	if err == nil {
		t.Fatal("production 缺 channelhealth signer 必须拒启")
	}
	// 两处审计私钥拒启共用 auditKeySetupHint,断言其关键指引齐全即同时覆盖两个调用点。
	for _, want := range []string{"openssl", "gen-audit-key.sh", "HUAKAI_AUDIT_PRIVATE_KEY_PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("审计私钥拒启文案应含指引 %q,实际:%q", want, err.Error())
		}
	}
}
