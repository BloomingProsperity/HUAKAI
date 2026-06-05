package pricingeval

import (
	"testing"

	"github.com/shopspring/decimal"
)

// TestApplyCacheCostOverride_DefaultIdentityIsZeroChange 默认(未设/倍率1)时
// 计费结果原样返回 —— 锁定"不设覆盖=官方价,行为零变化"。
func TestApplyCacheCostOverride_DefaultIdentityIsZeroChange(t *testing.T) {
	base := Result{
		Total:             decimal.RequireFromString("0.10"),
		CacheCreationCost: decimal.RequireFromString("0.03"),
		CacheReadCost:     decimal.RequireFromString("0.02"),
	}
	for _, ov := range []CacheCostOverride{
		{},                                  // 未设置
		{Multiplier: decimal.NewFromInt(1)}, // 显式 1.0
		{Multiplier: decimal.RequireFromString("0")},  // 非正 -> 按官方价
		{Multiplier: decimal.RequireFromString("-2")}, // 负 -> 按官方价
	} {
		got := ApplyCacheCostOverride(base, ov)
		if !got.Total.Equal(base.Total) ||
			!got.CacheCreationCost.Equal(base.CacheCreationCost) ||
			!got.CacheReadCost.Equal(base.CacheReadCost) {
			t.Fatalf("identity override %v changed result: %+v want %+v", ov, got, base)
		}
	}
}

// TestApplyCacheCostOverride_ScalesCacheAndAdjustsTotal 倍率 1.5 只缩放缓存两段,
// Total 按缓存成本变化量等额上调,非缓存分量不变。
func TestApplyCacheCostOverride_ScalesCacheAndAdjustsTotal(t *testing.T) {
	// input/output 贡献 = 0.10 - 0.03 - 0.02 = 0.05 (不该被覆盖触碰)
	base := Result{
		Total:             decimal.RequireFromString("0.10"),
		CacheCreationCost: decimal.RequireFromString("0.03"),
		CacheReadCost:     decimal.RequireFromString("0.02"),
	}
	got := ApplyCacheCostOverride(base, CacheCostOverride{Multiplier: decimal.RequireFromString("1.5")})

	if !got.CacheCreationCost.Equal(decimal.RequireFromString("0.045")) {
		t.Fatalf("CacheCreationCost=%s want 0.045", got.CacheCreationCost)
	}
	if !got.CacheReadCost.Equal(decimal.RequireFromString("0.03")) {
		t.Fatalf("CacheReadCost=%s want 0.03", got.CacheReadCost)
	}
	// delta = (0.045-0.03)+(0.03-0.02) = 0.015+0.01 = 0.025 -> Total 0.125
	if !got.Total.Equal(decimal.RequireFromString("0.125")) {
		t.Fatalf("Total=%s want 0.125 (only cache delta added)", got.Total)
	}
	// 判别:若错误地把 input/output 也乘了 1.5,Total 会是 0.15。
	if got.Total.Equal(decimal.RequireFromString("0.15")) {
		t.Fatal("Total=0.15 means non-cache cost was wrongly scaled")
	}
}
