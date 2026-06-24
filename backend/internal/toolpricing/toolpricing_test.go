package toolpricing_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// assertDecimal is a test helper that fails if got != want (string-parsed).
func assertDecimal(t *testing.T, label string, got decimal.Decimal, want string) {
	t.Helper()
	wantD := decimal.RequireFromString(want)
	if !got.Equal(wantD) {
		t.Fatalf("%s = %s, want %s", label, got.String(), wantD.String())
	}
}

// TestSurcharge_SpecExample verifies the canonical spec example:
// web_search count=3, price $10/1000, groupRatio 2.0 -> 0.06
//
// Derivation: ($10/1000) * 3 * 2.0 = $0.01 * 3 * 2.0 = $0.06
func TestSurcharge_SpecExample(t *testing.T) {
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 3}
	groupRatio := decimal.RequireFromString("2.0")

	got := toolpricing.Surcharge(prices, counts, groupRatio)
	assertDecimal(t, "Surcharge", got, "0.06")
}

// TestSurcharge_ZeroPrices_ReturnsZero ensures that when no tool prices are
// configured (the default empty-table case) the surcharge is exactly zero.
func TestSurcharge_ZeroPrices_ReturnsZero(t *testing.T) {
	prices := toolpricing.ToolPrices{} // all zero
	counts := toolpricing.ToolCallCounts{WebSearch: 5, FileSearch: 3, ImageGeneration: 1}
	got := toolpricing.Surcharge(prices, counts, decimal.NewFromInt(1))
	assertDecimal(t, "Surcharge", got, "0")
}

// TestSurcharge_ZeroCounts_ReturnsZero ensures that when there are no tool
// calls the surcharge is zero even if prices are configured.
func TestSurcharge_ZeroCounts_ReturnsZero(t *testing.T) {
	prices := toolpricing.ToolPrices{
		WebSearchPer1000:       decimal.RequireFromString("10"),
		FileSearchPer1000:      decimal.RequireFromString("5"),
		ImageGenerationPer1000: decimal.RequireFromString("40"),
	}
	counts := toolpricing.ToolCallCounts{} // all zero
	got := toolpricing.Surcharge(prices, counts, decimal.NewFromInt(1))
	assertDecimal(t, "Surcharge", got, "0")
}

// TestSurcharge_MultiTool_Sum verifies that multi-tool surcharges are summed
// and group-ratio is applied once to the total.
//
// web_search: ($10/1000)*2 = 0.02
// file_search: ($5/1000)*4  = 0.02
// image_gen:  ($40/1000)*1  = 0.04
// sum = 0.08; * groupRatio 1.5 = 0.12
func TestSurcharge_MultiTool_Sum(t *testing.T) {
	prices := toolpricing.ToolPrices{
		WebSearchPer1000:       decimal.RequireFromString("10"),
		FileSearchPer1000:      decimal.RequireFromString("5"),
		ImageGenerationPer1000: decimal.RequireFromString("40"),
	}
	counts := toolpricing.ToolCallCounts{
		WebSearch:       2,
		FileSearch:      4,
		ImageGeneration: 1,
	}
	groupRatio := decimal.RequireFromString("1.5")

	got := toolpricing.Surcharge(prices, counts, groupRatio)
	assertDecimal(t, "Surcharge", got, "0.12")
}

// TestSurcharge_DefaultGroupRatioZeroTreatedAsOne ensures that a zero groupRatio
// (the pricingeval default) is treated as 1 (identity) rather than zeroing the
// surcharge.
func TestSurcharge_DefaultGroupRatioZeroTreatedAsOne(t *testing.T) {
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 1}

	// zero groupRatio should be treated as 1.0
	got := toolpricing.Surcharge(prices, counts, decimal.Zero)
	assertDecimal(t, "Surcharge", got, "0.01")
}

// TestTable_EmptyTableReturnsZeroPrices verifies that a nil or empty Table
// returns zero ToolPrices for any lookup.
func TestTable_EmptyTableReturnsZeroPrices(t *testing.T) {
	var nilTable toolpricing.Table
	prices := nilTable.Lookup(42, "gpt-4o")
	if !prices.IsZero() {
		t.Fatalf("nil Table.Lookup returned non-zero prices: %+v", prices)
	}

	emptyTable := toolpricing.Table{}
	prices = emptyTable.Lookup(42, "gpt-4o")
	if !prices.IsZero() {
		t.Fatalf("empty Table.Lookup returned non-zero prices: %+v", prices)
	}
}

// TestTable_SetAndLookup verifies that prices stored via Set are returned by
// Lookup for the matching (tenantID, modelID) pair.
func TestTable_SetAndLookup(t *testing.T) {
	tbl := toolpricing.Table{}
	want := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	tbl.Set(7, "gpt-4o", want)

	got := tbl.Lookup(7, "gpt-4o")
	if !got.WebSearchPer1000.Equal(want.WebSearchPer1000) {
		t.Fatalf("Lookup WebSearchPer1000=%s want %s", got.WebSearchPer1000, want.WebSearchPer1000)
	}
	// different tenant/model should still return zero
	other := tbl.Lookup(99, "gpt-4o")
	if !other.IsZero() {
		t.Fatalf("cross-tenant lookup returned non-zero prices: %+v", other)
	}
}

// TestDefaultToolPrices verifies that DefaultToolPrices returns the expected
// platform-level constants: web_search=$10/1000, file_search=$2.5/1000,
// image_generation=$0 (Stage D deferred).
func TestDefaultToolPrices(t *testing.T) {
	p := toolpricing.DefaultToolPrices()
	assertDecimal(t, "WebSearchPer1000", p.WebSearchPer1000, "10")
	assertDecimal(t, "FileSearchPer1000", p.FileSearchPer1000, "2.5")
	if !p.ImageGenerationPer1000.IsZero() {
		t.Fatalf("ImageGenerationPer1000=%s want 0 (deferred Stage D)", p.ImageGenerationPer1000)
	}
}

// ---------------------------------------------------------------------------
// 止漏测试矩阵 E:platformSource.Lookup 两层回落 + defaults 价正确
// ---------------------------------------------------------------------------

// TestPlatformSource_FallsBackToDefaults 证明:无 override 命中时,Lookup 回落
// 平台默认价(用 DefaultToolPrices 时即 $10/$2.5/$0)。这是生产装配的常态路径
// (overrides=nil),也是「止漏」的关键——之前价表恒 nil 时这里返回零价(漏钱)。
//
// 变异:把 Lookup 的 return s.defaults 改成 return ToolPrices{}(退回漏钱行为)→
// web_search 价由 $10 变 $0,本测试 RED。
func TestPlatformSource_FallsBackToDefaults(t *testing.T) {
	src := toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), nil)
	got := src.Lookup(7, "gpt-4o")
	// defaults 价必须按官方默认对齐,且非零(否则又退回漏钱)。
	assertDecimal(t, "WebSearchPer1000(default回落)", got.WebSearchPer1000, "10")
	assertDecimal(t, "FileSearchPer1000(default回落)", got.FileSearchPer1000, "2.5")
	if !got.ImageGenerationPer1000.IsZero() {
		t.Fatalf("ImageGenerationPer1000=%s want 0", got.ImageGenerationPer1000)
	}
	if got.IsZero() {
		t.Fatal("platformSource 回落 defaults 不应返回全零价(漏钱回归)")
	}
}

// TestPlatformSource_OverrideWins 证明:override 命中(非零价)时用 override 价,
// 未命中的 (tenant,model) 仍回落 defaults。两条路径在同一测试里对照,确保
// override 分支与 defaults 分支都被覆盖且彼此判别性不同。
//
// 变异:把 Lookup 里 override 命中分支删掉(直接 return s.defaults)→ 命中条目
// 的价由 override 的 $25 变回 default 的 $10,本测试 RED。
func TestPlatformSource_OverrideWins(t *testing.T) {
	overrides := toolpricing.Table{}
	// 为 (tenant=7, gpt-4o) 设一个判别性 override:$25 ≠ default $10。
	overrides.Set(7, "gpt-4o", toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("25"),
	})
	src := toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), overrides)

	// 命中 override:用 $25,而不是 default 的 $10。
	hit := src.Lookup(7, "gpt-4o")
	assertDecimal(t, "命中override的WebSearchPer1000", hit.WebSearchPer1000, "25")

	// 未命中(其它 tenant):回落 default $10,证明回落分支仍活。
	miss := src.Lookup(99, "gpt-4o")
	assertDecimal(t, "未命中回落default的WebSearchPer1000", miss.WebSearchPer1000, "10")

	// 同一模型不同租户的两次查询结果必须不同,否则 override 分支是死代码。
	if hit.WebSearchPer1000.Equal(miss.WebSearchPer1000) {
		t.Fatal("override 命中价与回落 default 价相等 → override 分支非判别(死代码)")
	}
}

// TestPlatformSource_ZeroOverrideFallsBackToDefaults 证明:override 表里存了一个
// 全零价条目时,不把它当「有效覆盖」吞掉默认价,而是回落 defaults——否则运营者误存
// 零价条目会意外把某租户的工具调用重新变成 $0 漏钱。
//
// 变异:把 Lookup 的 !override.IsZero() 判定去掉(改成只要 overrides!=nil 命中就用)
// → 该零价条目会盖掉 default $10,本测试 RED。
func TestPlatformSource_ZeroOverrideFallsBackToDefaults(t *testing.T) {
	overrides := toolpricing.Table{}
	overrides.Set(7, "gpt-4o", toolpricing.ToolPrices{}) // 全零价条目
	src := toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), overrides)

	got := src.Lookup(7, "gpt-4o")
	assertDecimal(t, "零价override应回落default", got.WebSearchPer1000, "10")
}
