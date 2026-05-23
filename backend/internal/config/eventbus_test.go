package config

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadEventBusAllowMissingMoneyRefEnv(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "default false", raw: "", want: false},
		{name: "true literal", raw: "true", want: true},
		{name: "on literal", raw: "on", want: true},
		{name: "one literal", raw: "1", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEventBusEnv(t)
			t.Setenv(EnvTrustLedgerAllowMissingMoneyRef, tc.raw)

			cfg, err := LoadEventBus()
			if err != nil {
				t.Fatalf("LoadEventBus: %v", err)
			}
			if cfg.AllowMissingMoneyRef != tc.want {
				t.Fatalf("AllowMissingMoneyRef=%v want %v", cfg.AllowMissingMoneyRef, tc.want)
			}
		})
	}
}

func TestLoadEventBusAllowMissingMoneyRefInvalidFailsFast(t *testing.T) {
	clearEventBusEnv(t)
	t.Setenv(EnvTrustLedgerAllowMissingMoneyRef, "sometimes")

	_, err := LoadEventBus()
	if !errors.Is(err, ErrInvalidEventBusBool) {
		t.Fatalf("err=%v want ErrInvalidEventBusBool", err)
	}
	if !strings.Contains(err.Error(), EnvTrustLedgerAllowMissingMoneyRef) {
		t.Fatalf("err=%v must include env var %s", err, EnvTrustLedgerAllowMissingMoneyRef)
	}
}

func clearEventBusEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HUAKAI_EVENTBUS_ENABLED",
		"HUAKAI_EVENTBUS_HANDLER_TIMEOUT_SECONDS",
		"HUAKAI_EVENTBUS_SHUTDOWN_DRAIN_SECONDS",
		"HUAKAI_EVENTBUS_HIGH_WORKERS",
		"HUAKAI_EVENTBUS_MED_WORKERS",
		"HUAKAI_EVENTBUS_LOW_WORKERS",
		"HUAKAI_EVENTBUS_HIGH_BUFFER",
		"HUAKAI_EVENTBUS_MED_BUFFER",
		"HUAKAI_EVENTBUS_LOW_BUFFER",
		EnvTrustLedgerAllowMissingMoneyRef,
	} {
		t.Setenv(key, "")
	}
}
