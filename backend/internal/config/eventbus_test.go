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

func TestLoadEventBusAllowMissingMoneyRefProductionFailsFast(t *testing.T) {
	clearEventBusEnv(t)
	t.Setenv(EnvReleaseMode, "production")
	t.Setenv(EnvTrustLedgerAllowMissingMoneyRef, "true")

	cfg, err := LoadEventBus()
	if err == nil {
		t.Fatalf("LoadEventBus cfg=%+v err=nil; want production escape flag rejection", cfg)
	}
	if !errors.Is(err, ErrUnsafeEventBusConfig) {
		t.Fatalf("err=%v want ErrUnsafeEventBusConfig", err)
	}
	for _, want := range []string{EnvReleaseMode, EnvTrustLedgerAllowMissingMoneyRef, "production"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v must include %s", err, want)
		}
	}
}

// TestLoadEventBusMaxStatesEnv 守这个切片的核心行为变更:有限默认值关掉了状态 map 泄漏,
// 且 0/-1 作为"无界"逃生阀被原样透传(不被强制成有限值)。
// Mutation: 把默认 MaxStates:4096 改成 0(悄悄恢复泄漏,正是 Owner-gated 的默认翻转)→
// "default" 用例红;若 envEventBusStateCap 对 <=0 做了 coercion → "0"/"-1" 用例红。
func TestLoadEventBusMaxStatesEnv(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		raw  string
		want int
	}{
		{name: "default finite closes leak", set: false, want: 4096},
		{name: "explicit override", set: true, raw: "8192", want: 8192},
		{name: "zero is unbounded escape hatch", set: true, raw: "0", want: 0},
		{name: "negative is unbounded escape hatch", set: true, raw: "-1", want: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEventBusEnv(t)
			if tc.set {
				t.Setenv("HUAKAI_EVENTBUS_MAX_STATES", tc.raw)
			}
			cfg, err := LoadEventBus()
			if err != nil {
				t.Fatalf("LoadEventBus: %v", err)
			}
			if cfg.MaxStates != tc.want {
				t.Fatalf("MaxStates=%d want %d", cfg.MaxStates, tc.want)
			}
		})
	}
}

// TestLoadEventBusMaxStatesInvalidFailsFast 守非数值输入在启动时 fail-fast(不被吞掉)。
// Mutation: 把 envEventBusStateCap 改成 strconv.Atoi 后吞错返回 (n, nil) → err=nil → 红。
func TestLoadEventBusMaxStatesInvalidFailsFast(t *testing.T) {
	clearEventBusEnv(t)
	t.Setenv("HUAKAI_EVENTBUS_MAX_STATES", "notanumber")
	_, err := LoadEventBus()
	if !errors.Is(err, ErrInvalidEventBusStateCap) {
		t.Fatalf("err=%v want ErrInvalidEventBusStateCap", err)
	}
	if !strings.Contains(err.Error(), "HUAKAI_EVENTBUS_MAX_STATES") {
		t.Fatalf("err=%v must include the env var name", err)
	}
}

func clearEventBusEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		EnvReleaseMode,
		"HUAKAI_EVENTBUS_ENABLED",
		"HUAKAI_EVENTBUS_HANDLER_TIMEOUT_SECONDS",
		"HUAKAI_EVENTBUS_SHUTDOWN_DRAIN_SECONDS",
		"HUAKAI_EVENTBUS_HIGH_WORKERS",
		"HUAKAI_EVENTBUS_MED_WORKERS",
		"HUAKAI_EVENTBUS_LOW_WORKERS",
		"HUAKAI_EVENTBUS_HIGH_BUFFER",
		"HUAKAI_EVENTBUS_MED_BUFFER",
		"HUAKAI_EVENTBUS_LOW_BUFFER",
		"HUAKAI_EVENTBUS_MAX_STATES",
		EnvTrustLedgerAllowMissingMoneyRef,
	} {
		t.Setenv(key, "")
	}
}
