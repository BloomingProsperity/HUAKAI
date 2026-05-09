// pool_selector_test.go — PASR-lite main-wire M1 atomic 单测。
//
// 覆盖矩阵:
//   - ParsePoolSelectorMode: 5 合法值, 大小写, 前后空格, 非法字符串
//   - LoadPoolSelector: 默认值, 全 ENV 设置, 单字段设置, 非法 mode, percent 越界,
//     percent 非数字
//   - 谓词方法 IsPASR / NeedsShadowInstance / NeedsActualPASR 跨 5 mode 矩阵
package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePoolSelectorMode_AllValid(t *testing.T) {
	cases := []struct {
		raw  string
		want PoolSelectorMode
	}{
		{"default", PoolSelectorModeDefault},
		{"shadow", PoolSelectorModeShadow},
		{"canary", PoolSelectorModeCanary},
		{"pasr-primary", PoolSelectorModePASRPrimary},
		{"pasr-strict", PoolSelectorModePASRStrict},
	}
	for _, tc := range cases {
		got, err := ParsePoolSelectorMode(tc.raw)
		if err != nil {
			t.Errorf("ParsePoolSelectorMode(%q) err=%v, want nil", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParsePoolSelectorMode(%q)=%q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParsePoolSelectorMode_CaseInsensitiveAndTrim(t *testing.T) {
	cases := []string{"DEFAULT", "Shadow", "  canary  ", "PASR-Primary", "\tpasr-strict\n"}
	for _, raw := range cases {
		_, err := ParsePoolSelectorMode(raw)
		if err != nil {
			t.Errorf("ParsePoolSelectorMode(%q) err=%v, want nil (大小写/空格容错)", raw, err)
		}
	}
}

func TestParsePoolSelectorMode_InvalidReturnsTypedError(t *testing.T) {
	cases := []string{"", "off", "pasr", "shadow_5", "default ", "canary-5", "PASR"}
	for _, raw := range cases {
		// "default " 经 TrimSpace 后变成 "default" 合法, 验证 trim 行为生效。
		// "PASR" 经 ToLower 后变成 "pasr" 不在集合内, 应失败。
		_, err := ParsePoolSelectorMode(raw)
		switch raw {
		case "default ":
			if err != nil {
				t.Errorf("ParsePoolSelectorMode(%q) err=%v, want nil (trim)", raw, err)
			}
		default:
			if err == nil {
				t.Errorf("ParsePoolSelectorMode(%q) err=nil, want error", raw)
				continue
			}
			if !errors.Is(err, ErrInvalidPoolSelectorMode) {
				t.Errorf("ParsePoolSelectorMode(%q) err=%v, want ErrInvalidPoolSelectorMode", raw, err)
			}
			if !strings.Contains(err.Error(), "expected one of") {
				t.Errorf("ParsePoolSelectorMode(%q) err msg %q lacks 'expected one of' hint", raw, err.Error())
			}
		}
	}
}

func TestLoadPoolSelector_DefaultsWhenEnvUnset(t *testing.T) {
	clearPoolSelectorEnv(t)
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("LoadPoolSelector err=%v want nil", err)
	}
	if cfg.Mode != PoolSelectorModeDefault {
		t.Errorf("Mode=%q want %q (default)", cfg.Mode, PoolSelectorModeDefault)
	}
	if cfg.ShadowPercent != 0 {
		t.Errorf("ShadowPercent=%d want 0", cfg.ShadowPercent)
	}
	if cfg.CanaryPercent != 0 {
		t.Errorf("CanaryPercent=%d want 0", cfg.CanaryPercent)
	}
	if cfg.SamplingSalt != defaultPoolSelectorSalt {
		t.Errorf("SamplingSalt=%q want %q", cfg.SamplingSalt, defaultPoolSelectorSalt)
	}
}

func TestLoadPoolSelector_AllEnvSet(t *testing.T) {
	clearPoolSelectorEnv(t)
	t.Setenv("HUAKAI_POOL_SELECTOR_MODE", "shadow")
	t.Setenv("HUAKAI_POOL_SELECTOR_SHADOW_PCT", "25")
	t.Setenv("HUAKAI_POOL_SELECTOR_CANARY_PCT", "5")
	t.Setenv("HUAKAI_POOL_SELECTOR_SALT", "ops-2026-05-08-r1")

	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("LoadPoolSelector err=%v want nil", err)
	}
	if cfg.Mode != PoolSelectorModeShadow {
		t.Errorf("Mode=%q want shadow", cfg.Mode)
	}
	if cfg.ShadowPercent != 25 {
		t.Errorf("ShadowPercent=%d want 25", cfg.ShadowPercent)
	}
	if cfg.CanaryPercent != 5 {
		t.Errorf("CanaryPercent=%d want 5", cfg.CanaryPercent)
	}
	if cfg.SamplingSalt != "ops-2026-05-08-r1" {
		t.Errorf("SamplingSalt=%q want ops-2026-05-08-r1", cfg.SamplingSalt)
	}
}

func TestLoadPoolSelector_InvalidModeFailsFast(t *testing.T) {
	clearPoolSelectorEnv(t)
	t.Setenv("HUAKAI_POOL_SELECTOR_MODE", "off")

	_, err := LoadPoolSelector()
	if err == nil {
		t.Fatalf("LoadPoolSelector err=nil want error (mode=off)")
	}
	if !errors.Is(err, ErrInvalidPoolSelectorMode) {
		t.Errorf("err=%v want wraps ErrInvalidPoolSelectorMode", err)
	}
}

func TestLoadPoolSelector_PercentBoundaries(t *testing.T) {
	type perCase struct {
		envKey string
		setter func(*PoolSelectorConfig) int
	}
	cases := []perCase{
		{"HUAKAI_POOL_SELECTOR_SHADOW_PCT", func(c *PoolSelectorConfig) int { return c.ShadowPercent }},
		{"HUAKAI_POOL_SELECTOR_CANARY_PCT", func(c *PoolSelectorConfig) int { return c.CanaryPercent }},
	}

	for _, pc := range cases {
		// 0 / 100 边界合法
		for _, v := range []string{"0", "100"} {
			clearPoolSelectorEnv(t)
			t.Setenv(pc.envKey, v)
			cfg, err := LoadPoolSelector()
			if err != nil {
				t.Errorf("[%s=%s] err=%v want nil", pc.envKey, v, err)
				continue
			}
			want := 0
			if v == "100" {
				want = 100
			}
			if pc.setter(cfg) != want {
				t.Errorf("[%s=%s] got %d want %d", pc.envKey, v, pc.setter(cfg), want)
			}
		}
		// -1 / 101 / abc / 空数字非法
		for _, v := range []string{"-1", "101", "abc", "1.5", "200"} {
			clearPoolSelectorEnv(t)
			t.Setenv(pc.envKey, v)
			_, err := LoadPoolSelector()
			if err == nil {
				t.Errorf("[%s=%s] err=nil want error", pc.envKey, v)
				continue
			}
			if !errors.Is(err, ErrInvalidPercent) {
				t.Errorf("[%s=%s] err=%v want wraps ErrInvalidPercent", pc.envKey, v, err)
			}
		}
	}
}

func TestPoolSelectorConfig_Predicates(t *testing.T) {
	cases := []struct {
		mode            PoolSelectorMode
		isPASR          bool
		needsShadowInst bool
		needsActualPASR bool
	}{
		{PoolSelectorModeDefault, false, false, false},
		{PoolSelectorModeShadow, true, true, false},
		{PoolSelectorModeCanary, true, false, true},
		{PoolSelectorModePASRPrimary, true, false, true},
		{PoolSelectorModePASRStrict, true, false, true},
	}
	for _, tc := range cases {
		cfg := &PoolSelectorConfig{Mode: tc.mode}
		if got := cfg.IsPASR(); got != tc.isPASR {
			t.Errorf("[%s] IsPASR=%v want %v", tc.mode, got, tc.isPASR)
		}
		if got := cfg.NeedsShadowInstance(); got != tc.needsShadowInst {
			t.Errorf("[%s] NeedsShadowInstance=%v want %v", tc.mode, got, tc.needsShadowInst)
		}
		if got := cfg.NeedsActualPASR(); got != tc.needsActualPASR {
			t.Errorf("[%s] NeedsActualPASR=%v want %v", tc.mode, got, tc.needsActualPASR)
		}
	}
}

func TestPoolSelectorConfig_Validate(t *testing.T) {
	// 合法构造
	for _, m := range validPoolSelectorModes {
		cfg := &PoolSelectorConfig{Mode: m, ShadowPercent: 50, CanaryPercent: 5, SamplingSalt: "x"}
		if err := cfg.Validate(); err != nil {
			t.Errorf("[%s] Validate err=%v want nil", m, err)
		}
	}
	// nil 接收者
	var nilCfg *PoolSelectorConfig
	if err := nilCfg.Validate(); !errors.Is(err, ErrInvalidPoolSelectorMode) {
		t.Errorf("nil cfg Validate err=%v want wraps ErrInvalidPoolSelectorMode", err)
	}
	// 非法 Mode (struct literal 注入路径绕过 ENV)
	cfg := &PoolSelectorConfig{Mode: "bogus"}
	if err := cfg.Validate(); !errors.Is(err, ErrInvalidPoolSelectorMode) {
		t.Errorf("bogus mode Validate err=%v want wraps ErrInvalidPoolSelectorMode", err)
	}
	// percent 越界
	for _, pct := range []int{-1, 101} {
		bad := &PoolSelectorConfig{Mode: PoolSelectorModeShadow, ShadowPercent: pct}
		if err := bad.Validate(); !errors.Is(err, ErrInvalidPercent) {
			t.Errorf("ShadowPercent=%d Validate err=%v want wraps ErrInvalidPercent", pct, err)
		}
		bad2 := &PoolSelectorConfig{Mode: PoolSelectorModeShadow, CanaryPercent: pct}
		if err := bad2.Validate(); !errors.Is(err, ErrInvalidPercent) {
			t.Errorf("CanaryPercent=%d Validate err=%v want wraps ErrInvalidPercent", pct, err)
		}
	}
}

// clearPoolSelectorEnv 把四个 PASR 相关 ENV 显式置空, 避免父进程或前一测试
// 残留的 ENV 污染当前用例。 LoadPoolSelector 把空字符串视为未设置 (走默认值),
// 所以"置空"在语义上等价于"未设置"。 t.Setenv 注册的还原会在 t.Cleanup
// 自动触发, 不影响并发测试。
func clearPoolSelectorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HUAKAI_POOL_SELECTOR_MODE",
		"HUAKAI_POOL_SELECTOR_SHADOW_PCT",
		"HUAKAI_POOL_SELECTOR_CANARY_PCT",
		"HUAKAI_POOL_SELECTOR_SALT",
	} {
		t.Setenv(k, "")
	}
}
