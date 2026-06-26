package main

import "testing"

// TestValidateStormScopePairRejectsHalfConfig 证明半配置的
// refresh-storm 作用域是启动错误,而非被悄悄禁用的限流:只设了 rate(忘了 burst)、
// 只设了 burst,或设了不足一个单位的 burst,都必须显式报错,
// 这样一处拼写失误就不会让跨账号的洪峰不受限流地放行。两者都不设
//(有意关闭该作用域)以及完整的 rate+burst 配对都必须通过。
//
// 变异:在半配置的 default 分支返回 nil → 三条
// 拒绝断言即转红。
func TestValidateStormScopePairRejectsHalfConfig(t *testing.T) {
	if err := validateStormScopePair("endpoint", 5, 0); err == nil {
		t.Fatal("rate-only config must be rejected (burst missing)")
	}
	if err := validateStormScopePair("endpoint", 0, 5); err == nil {
		t.Fatal("burst-only config must be rejected (rate missing)")
	}
	if err := validateStormScopePair("endpoint", 5, 0.5); err == nil {
		t.Fatal("sub-unit burst must be rejected (cannot admit a whole token)")
	}
	if err := validateStormScopePair("endpoint", 0, 0); err != nil {
		t.Fatalf("both-unset must be allowed (scope off): %v", err)
	}
	if err := validateStormScopePair("endpoint", 5, 2); err != nil {
		t.Fatalf("fully-configured pair must be allowed: %v", err)
	}
}

// TestLoadStormScopeConfigFromEnvParsesAndFailsLoud 证明环境变量加载器
// 会解析一个合法配对,并在半配置作用域上显式报错。变异:去掉
// loadStormScopeConfigFromEnv 里对 validateStormScopePair 的调用 → 半配置用例
// 返回 nil error → 转红。
func TestLoadStormScopeConfigFromEnvParsesAndFailsLoud(t *testing.T) {
	// 隔离性:加载器会读取全部四个 storm 变量,所以把每一个都钉死——把 global 配对清成 ""——
	// 而不是去继承宿主/CI 环境里恰好设置的任何值。
	t.Setenv("HUAKAI_STORM_ENDPOINT_RATE", "3")
	t.Setenv("HUAKAI_STORM_ENDPOINT_BURST", "6")
	t.Setenv("HUAKAI_STORM_GLOBAL_RATE", "")
	t.Setenv("HUAKAI_STORM_GLOBAL_BURST", "")
	cfg, err := loadStormScopeConfigFromEnv()
	if err != nil {
		t.Fatalf("valid endpoint config load: %v", err)
	}
	if cfg.PerEndpointRate != 3 || cfg.PerEndpointBurst != 6 {
		t.Fatalf("parsed cfg=%+v, want endpoint rate/burst 3/6", cfg)
	}

	// global rate 已设但 burst 未设 → 半配置 → 必须显式报错。
	t.Setenv("HUAKAI_STORM_GLOBAL_RATE", "10")
	if _, err := loadStormScopeConfigFromEnv(); err == nil {
		t.Fatal("half-configured global scope (rate without burst) must fail loud at load")
	}
}

// TestParseStormFloatEnvRejectsGarbage 证明格式错误的预算值会显式报错,而非
// 降级为 0(静默禁用)或启动一个无上限的限流。Inf 与 NaN
// 是微妙之处:strconv.ParseFloat 接受它们且*不*报错。
// 变异:去掉 math.IsInf/IsNaN 守护 → Inf/NaN 用例返回 nil error → 转红。
func TestParseStormFloatEnvRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"not-a-number", "-2", "Inf", "+Inf", "NaN"} {
		t.Setenv("HUAKAI_STORM_ENDPOINT_RATE", bad)
		if _, err := parseStormFloatEnv("HUAKAI_STORM_ENDPOINT_RATE"); err == nil {
			t.Fatalf("storm budget %q must be rejected, got nil error", bad)
		}
	}
}
