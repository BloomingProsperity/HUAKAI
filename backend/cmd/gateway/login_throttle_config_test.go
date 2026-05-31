// HUAKAI · iKun

package main

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
)

// TestLoginThrottleConfigFromEnv_Defaults 钉住:无 env 时回落到 DefaultConfig。
// mutation: loader 不从 DefaultConfig 起步 → 字段不匹配 → 红。
func TestLoginThrottleConfigFromEnv_Defaults(t *testing.T) {
	cfg, err := loginThrottleConfigFromEnv()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	def := loginthrottle.DefaultConfig()
	if cfg.InFlightLimit != def.InFlightLimit || cfg.Window != def.Window ||
		cfg.WindowLimit != def.WindowLimit || cfg.BanWindow != def.BanWindow ||
		cfg.BanAfter != def.BanAfter || cfg.BanDuration != def.BanDuration ||
		cfg.MaxKeys != def.MaxKeys {
		t.Fatalf("no-env config = %+v, want defaults %+v", cfg, def)
	}
}

// TestLoginThrottleConfigFromEnv_ValidOverride 钉住:合法 env 覆盖被应用。
// mutation: loader 忽略 env(返回写死默认)→ 覆盖值不生效 → 红。
func TestLoginThrottleConfigFromEnv_ValidOverride(t *testing.T) {
	t.Setenv(loginThrottleInFlightEnv, "7")
	t.Setenv(loginThrottleWindowEnv, "30s")
	t.Setenv(loginThrottleBanAfterEnv, "13")
	cfg, err := loginThrottleConfigFromEnv()
	if err != nil {
		t.Fatalf("valid override: %v", err)
	}
	if cfg.InFlightLimit != 7 {
		t.Fatalf("InFlightLimit = %d, want 7 (env override ignored)", cfg.InFlightLimit)
	}
	if cfg.Window != 30*time.Second {
		t.Fatalf("Window = %s, want 30s (env override ignored)", cfg.Window)
	}
	if cfg.BanAfter != 13 {
		t.Fatalf("BanAfter = %d, want 13 (env override ignored)", cfg.BanAfter)
	}
}

// TestLoginThrottleConfigFromEnv_InvalidFailsFast 钉住:非法 env 必须 fail-fast(返回错误),
// 绝不静默禁用限流 —— 这正是 codex 复审强调的「配错 silently disable throttle」风险。
// mutation: loader 吞掉解析错误 / 接受非正值 → 不返回错误 → 红(登录 DoS 防护被悄悄关掉)。
func TestLoginThrottleConfigFromEnv_InvalidFailsFast(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"non_integer_inflight", loginThrottleInFlightEnv, "abc"},
		{"zero_inflight", loginThrottleInFlightEnv, "0"},
		{"negative_window_limit", loginThrottleWindowLimitEnv, "-3"},
		{"bad_duration_window", loginThrottleWindowEnv, "not-a-duration"},
		{"zero_ban_duration", loginThrottleBanDurEnv, "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			if _, err := loginThrottleConfigFromEnv(); err == nil {
				t.Fatalf("%s=%q must fail-fast (return error), got nil — throttle silently misconfigured/disabled", tc.key, tc.val)
			}
		})
	}
}
