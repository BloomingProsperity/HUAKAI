package main

import (
	"strings"
	"testing"
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
