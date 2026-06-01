package main

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// TestValidateReleaseMode_RejectsMisspelledProduction guards S1-019:
// a SET-but-unrecognized HUAKAI_RELEASE_MODE (e.g. an operator typo "prod"
// meant as production) must FAIL CLOSED at startup, not silently degrade to dev.
// Before the fix, releaseModeProduction() exact-matched "production", so "prod"
// returned false → dev path with ephemeral keys / memory ledger / skipped gates,
// and the service still booted with no error.
//
// Mutation check: delete validateReleaseMode's `default` error branch (return nil
// for everything) and the misspelled cases below go green → test fails. The empty
// and recognized cases prove we did NOT break the established "空=dev" convention
// that existing tests (wiring_test.go uses HUAKAI_RELEASE_MODE="") depend on.
func TestValidateReleaseMode_RejectsMisspelledProduction(t *testing.T) {
	allowed := []string{"", "dev", "development", "DEV", "test", "production", "Production", " production "}
	for _, v := range allowed {
		v := v
		t.Run("allow/"+v, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", v)
			if err := validateReleaseMode(); err != nil {
				t.Fatalf("validateReleaseMode(%q) = %v, want nil (recognized mode must boot)", v, err)
			}
		})
	}

	// Realistic production-intent typos that previously silently became dev.
	rejected := []string{"prod", "PROD", "produciton", "prd", "prod-us", "release", "staging", "1"}
	for _, v := range rejected {
		v := v
		t.Run("reject/"+v, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", v)
			err := validateReleaseMode()
			if err == nil {
				t.Fatalf("validateReleaseMode(%q) = nil, want fail-closed error (silent dev degradation)", v)
			}
			if !strings.Contains(err.Error(), v) {
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

// TestValidateDevAuthTokenFlag guards S1-018: HUAKAI_DEV_AUTH_RETURN_TOKEN=true echoes the raw
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
		t.Setenv("HUAKAI_RELEASE_MODE", "")
		t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
		if err := validateDevAuthTokenFlag(); err != nil {
			t.Fatalf("dev mode with the flag must still boot (CI/local ergonomics); got %v", err)
		}
	})
}

// TestLoadUserRegistrationModeFromEnv guards S2-012: production startup must not leave public
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
