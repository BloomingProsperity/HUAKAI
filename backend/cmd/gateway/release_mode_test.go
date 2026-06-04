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

// TestValidateReleaseMode_RequiresExplicitMode guards that the gateway
// must not boot with an omitted or unrecognized HUAKAI_RELEASE_MODE because
// that silently selects the dev path with ephemeral keys, memory ledger, and
// skipped release gates.
//
// Mutation check: allow the empty string again in validateReleaseMode and the
// missing_env case below fails while explicit dev/test modes still prove local
// and CI runs have a supported non-production path.
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

// TestValidateReleaseMode_TypoDoesNotEnableProduction proves the *consequence*:
// a misspelled production value is both rejected by validateReleaseMode AND would
// not have flipped releaseModeProduction() to true — i.e. it really is the silent
// dev-degradation path the gate now closes.
func TestValidateReleaseMode_TypoDoesNotEnableProduction(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "prod")
	if releaseModeProduction() {
		t.Fatal("guard precondition broken: \"prod\" should not satisfy releaseModeProduction()")
	}
	if err := validateReleaseMode(); err == nil {
		t.Fatal("\"prod\" must be rejected at startup so it cannot silently run as dev")
	}
}

// TestValidateDevAuthTokenFlag guards that HUAKAI_DEV_AUTH_RETURN_TOKEN=true echoes the raw
// one-time verification/reset secret into the public register/reset JSON response. In production it
// must FAIL CLOSED at startup, not merely log a warning and boot (the prior behavior).
//
// Mutation check: delete the production+flag guard in validateDevAuthTokenFlag (always return nil);
// the prod+flag case goes green → red. The dev/no-flag cases prove local/CI ergonomics still boot.
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

// TestProductionCaptchaConfigWarnsAndMarksHealthWhenSecretMissing guards the
// availability correction: a missing Turnstile secret must not fail production
// boot, but it must produce an operator-visible WARN and degraded config status.
// Mutation check: restore fail-boot, drop the WARN, or mark configuration OK;
// this test goes red on the exact over-fix or silent-misconfig regression.
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

// TestLoadUserRegistrationModeFromEnv guards that production startup must not leave public
// registration open just because no operator explicitly configured a registration policy. Dev/test keep
// the historical open default, while production defaults to disabled until the operator opts into open
// or invite-required registration.
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
