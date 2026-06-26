package main

import (
	"testing"
	"time"
)

// TestHermesMutateGuardConfig_UnsetIsDefaults 证明：在未设置任何
// HUAKAI_HERMES_MUTATE_* 环境变量时，解析出的 config 即为保守的内置
// 默认值——并且由它构建出的 orchestrator options + limiter 是
// 启用（ENABLED，部署默认）而非禁用的。变异：把某个默认值改成 0
//（其禁用哨兵值），对应的断言就会变红（RED）。
func TestHermesMutateGuardConfig_UnsetIsDefaults(t *testing.T) {
	for _, env := range []string{
		hermesMutateMaxConcurrencyEnv, hermesMutateAcquireWaitEnv,
		hermesMutateTxDeadlineEnv, hermesMutateRatePerTokenEnv, hermesMutateRateWindowEnv,
	} {
		t.Setenv(env, "")
	}
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("unset config err=%v want nil", err)
	}
	if cfg.maxConcurrency != defaultHermesMutateMaxConcurrency {
		t.Fatalf("maxConcurrency=%d want %d", cfg.maxConcurrency, defaultHermesMutateMaxConcurrency)
	}
	if cfg.acquireWait != defaultHermesMutateAcquireWait {
		t.Fatalf("acquireWait=%v want %v", cfg.acquireWait, defaultHermesMutateAcquireWait)
	}
	if cfg.txDeadline != defaultHermesMutateTxDeadline {
		t.Fatalf("txDeadline=%v want %v (90s, 3x the 30s dlq lease)", cfg.txDeadline, defaultHermesMutateTxDeadline)
	}
	if cfg.txDeadline != 90*time.Second {
		t.Fatalf("txDeadline default=%v want exactly 90s — do NOT tighten (load-bearing headroom)", cfg.txDeadline)
	}
	if cfg.ratePerToken != defaultHermesMutateRatePerToken {
		t.Fatalf("ratePerToken=%d want %d", cfg.ratePerToken, defaultHermesMutateRatePerToken)
	}
	if cfg.rateWindow != defaultHermesMutateRateWindow {
		t.Fatalf("rateWindow=%v want %v", cfg.rateWindow, defaultHermesMutateRateWindow)
	}
	if l := cfg.newRateLimiter(); !l.Enabled() {
		t.Fatalf("default rate limiter disabled — default must be ENABLED (per_token=30)")
	}
}

// TestHermesMutateGuardConfig_ZeroDisablesEachGuard 证明显式置 0 的禁用
// 哨兵值：运维把某个旋钮设为 0，就恰好关闭对应的那一项守卫。
func TestHermesMutateGuardConfig_ZeroDisablesEachGuard(t *testing.T) {
	t.Setenv(hermesMutateMaxConcurrencyEnv, "0")
	t.Setenv(hermesMutateTxDeadlineEnv, "0")
	t.Setenv(hermesMutateRatePerTokenEnv, "0")
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("zero-knobs config err=%v want nil", err)
	}
	if cfg.maxConcurrency != 0 {
		t.Fatalf("maxConcurrency=%d want 0 (disabled)", cfg.maxConcurrency)
	}
	if cfg.txDeadline != 0 {
		t.Fatalf("txDeadline=%v want 0 (disabled)", cfg.txDeadline)
	}
	if cfg.ratePerToken != 0 {
		t.Fatalf("ratePerToken=%d want 0 (disabled)", cfg.ratePerToken)
	}
	if l := cfg.newRateLimiter(); l.Enabled() {
		t.Fatalf("rate limiter enabled with per_token=0 — 0 must disable it")
	}
}

// TestHermesMutateGuardConfig_FailsLoudOnGarbage 证明：格式错误的旋钮值会
// 触发启动报错，绝不会静默回退（那可能给路径设错边界）。
func TestHermesMutateGuardConfig_FailsLoudOnGarbage(t *testing.T) {
	t.Setenv(hermesMutateMaxConcurrencyEnv, "not-a-number")
	if _, err := hermesMutateGuardConfigFromEnv(); err == nil {
		t.Fatalf("malformed max_concurrency did not fail loud")
	}
	t.Setenv(hermesMutateMaxConcurrencyEnv, "4")
	t.Setenv(hermesMutateTxDeadlineEnv, "garbage")
	if _, err := hermesMutateGuardConfigFromEnv(); err == nil {
		t.Fatalf("malformed tx_deadline did not fail loud")
	}
}

// TestHermesMutateGuardConfig_ParsesDurationOrSeconds 证明两种形式都能解析。
func TestHermesMutateGuardConfig_ParsesDurationOrSeconds(t *testing.T) {
	t.Setenv(hermesMutateTxDeadlineEnv, "120s")
	t.Setenv(hermesMutateRateWindowEnv, "30") // 裸秒数
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if cfg.txDeadline != 120*time.Second {
		t.Fatalf("txDeadline=%v want 120s", cfg.txDeadline)
	}
	if cfg.rateWindow != 30*time.Second {
		t.Fatalf("rateWindow=%v want 30s", cfg.rateWindow)
	}
}
