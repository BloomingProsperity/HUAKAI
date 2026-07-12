package pricingeval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// TestApplyToolCallSurcharge_DefaultZero_EmptyPrices 验证首要安全不变量:
// 空的 ToolPrices(默认——未配置)使 Total 与仅按 token 计费的结果逐字节一致。
func TestApplyToolCallSurcharge_DefaultZero_EmptyPrices(t *testing.T) {
	base := Result{
		Total:             decimal.RequireFromString("0.10"),
		CacheCreationCost: decimal.RequireFromString("0.03"),
		CacheReadCost:     decimal.RequireFromString("0.02"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 5}
	prices := toolpricing.ToolPrices{} // 零值——未配置

	got := ApplyToolCallSurcharge(base, prices, counts, decimal.NewFromInt(1))
	if !got.Total.Equal(base.Total) {
		t.Fatalf("empty prices: Total=%s want %s (must be unchanged)", got.Total, base.Total)
	}
}

// TestApplyToolCallSurcharge_DefaultZero_ZeroCounts 验证第二条安全不变量:
// ToolCallCounts 为零(没有任何工具调用)时,即便配置了价格,Total 也保持不变。
func TestApplyToolCallSurcharge_DefaultZero_ZeroCounts(t *testing.T) {
	base := Result{
		Total: decimal.RequireFromString("0.10"),
	}
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{} // 零值

	got := ApplyToolCallSurcharge(base, prices, counts, decimal.NewFromInt(1))
	if !got.Total.Equal(base.Total) {
		t.Fatalf("zero counts: Total=%s want %s (must be unchanged)", got.Total, base.Total)
	}
}

// TestApplyToolCallSurcharge_AddsDeltaToTotal 验证当价格和次数都非零时,
// Total 恰好增加附加费的增量。
//
// 配置:tokenTotal=0.10,web_search 次数=3,价格=$10/1000,groupRatio=1.0
// 预期:增量 = ($10/1000)*3*1 = $0.03 -> Total = 0.13
func TestApplyToolCallSurcharge_AddsDeltaToTotal(t *testing.T) {
	base := Result{
		Total:             decimal.RequireFromString("0.10"),
		CacheCreationCost: decimal.RequireFromString("0.03"),
		CacheReadCost:     decimal.RequireFromString("0.02"),
	}
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 3}

	got := ApplyToolCallSurcharge(base, prices, counts, decimal.NewFromInt(1))
	assertPricingDecimal(t, "Total", got.Total, "0.13")
	// 非 Total 字段必须保持不变。
	assertPricingDecimal(t, "CacheCreationCost", got.CacheCreationCost, "0.03")
	assertPricingDecimal(t, "CacheReadCost", got.CacheReadCost, "0.02")
}

// TestApplyToolCallSurcharge_GroupRatioScalesSurcharge 验证 groupRatio
// 会被施加到附加费上(与 cache_override + resolver 的 GroupRatio 语义一致)。
//
// 配置:web_search 次数=3,价格=$10/1000,groupRatio=2.0
// 附加费 = ($10/1000)*3*2.0 = $0.06 -> Total = 0.10 + 0.06 = 0.16
func TestApplyToolCallSurcharge_GroupRatioScalesSurcharge(t *testing.T) {
	base := Result{Total: decimal.RequireFromString("0.10")}
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 3}
	groupRatio := decimal.RequireFromString("2.0")

	got := ApplyToolCallSurcharge(base, prices, counts, groupRatio)
	assertPricingDecimal(t, "Total", got.Total, "0.16")
}

// TestResolve_WithToolCallSurcharge_TotalEqualsTokenPlusSurcharge 通过一个施加
// 附加费步骤的辅助函数,跑通完整的 Resolve 路径。
// 这证明了端到端集成:tokenTotal + 附加费。
//
// Token 计费:10 个 input token * 1000 micro_usd/token = 10000 micros
//
//	= 10000 / 1_000_000 = 0.01 USD(flat,groupRatio=1)
//
// 附加费:web_search 次数=3,$10/1000,groupRatio=1 -> 0.03
// 预期 Total:0.04
func TestResolve_WithToolCallSurcharge_TotalEqualsTokenPlusSurcharge(t *testing.T) {
	raw := json.RawMessage(`{"input_micro_usd":1000}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	usage := Usage{
		InputTokens:    10,
		ToolCallCounts: toolpricing.ToolCallCounts{WebSearch: 3},
	}
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}

	res, err := Resolve(context.Background(), raw, usage, flat, "price-v1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// 施加附加费步骤(与 chat_completions_pricing 的用法一致)。
	res = ApplyToolCallSurcharge(res, prices, usage.ToolCallCounts, flat.GroupRatio)

	assertPricingDecimal(t, "Total", res.Total, "0.04")
	// 变异守卫:仅按 token 计费的总额是 0.01,而非 0.04。
	if res.Total.Equal(decimal.RequireFromString("0.01")) {
		t.Fatal("surcharge was not added: Total equals token-only 0.01")
	}
}

// TestResolve_WithEmptyToolPrices_TotalUnchanged 证明默认零值路径:
// 空的 ToolPrices -> Total == tokenTotal(不影响现有计费)。
func TestResolve_WithEmptyToolPrices_TotalUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"input_micro_usd":1000}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	usage := Usage{
		InputTokens:    10,
		ToolCallCounts: toolpricing.ToolCallCounts{WebSearch: 5},
	}
	emptyPrices := toolpricing.ToolPrices{} // 默认: 未配置任何 tool 价格

	res, err := Resolve(context.Background(), raw, usage, flat, "price-v1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	tokenTotal := res.Total
	res = ApplyToolCallSurcharge(res, emptyPrices, usage.ToolCallCounts, flat.GroupRatio)

	if !res.Total.Equal(tokenTotal) {
		t.Fatalf("empty prices: Total=%s want %s (must equal tokenTotal)", res.Total, tokenTotal)
	}
}

// TestResolve_WithZeroToolCallCounts_TotalUnchanged 证明当 ToolCallCounts 为
// 零值(默认 struct)时走的默认零值路径。
func TestResolve_WithZeroToolCallCounts_TotalUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"input_micro_usd":1000}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	usage := Usage{InputTokens: 10} // ToolCallCounts 默认为零值
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}

	res, err := Resolve(context.Background(), raw, usage, flat, "price-v1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	tokenTotal := res.Total
	res = ApplyToolCallSurcharge(res, prices, usage.ToolCallCounts, flat.GroupRatio)

	if !res.Total.Equal(tokenTotal) {
		t.Fatalf("zero counts: Total=%s want %s (must equal tokenTotal)", res.Total, tokenTotal)
	}
}
