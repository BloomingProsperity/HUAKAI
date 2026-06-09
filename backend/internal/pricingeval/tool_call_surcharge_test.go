package pricingeval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// TestApplyToolCallSurcharge_DefaultZero_EmptyPrices verifies the primary
// safety invariant: an empty ToolPrices (default — no config) leaves Total
// byte-identical to the token-only result.
func TestApplyToolCallSurcharge_DefaultZero_EmptyPrices(t *testing.T) {
	base := Result{
		Total:             decimal.RequireFromString("0.10"),
		CacheCreationCost: decimal.RequireFromString("0.03"),
		CacheReadCost:     decimal.RequireFromString("0.02"),
	}
	counts := toolpricing.ToolCallCounts{WebSearch: 5}
	prices := toolpricing.ToolPrices{} // zero — unconfigured

	got := ApplyToolCallSurcharge(base, prices, counts, decimal.NewFromInt(1))
	if !got.Total.Equal(base.Total) {
		t.Fatalf("empty prices: Total=%s want %s (must be unchanged)", got.Total, base.Total)
	}
}

// TestApplyToolCallSurcharge_DefaultZero_ZeroCounts verifies the second safety
// invariant: zero ToolCallCounts (no tool calls) leaves Total unchanged even
// when prices are configured.
func TestApplyToolCallSurcharge_DefaultZero_ZeroCounts(t *testing.T) {
	base := Result{
		Total: decimal.RequireFromString("0.10"),
	}
	prices := toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	}
	counts := toolpricing.ToolCallCounts{} // zero

	got := ApplyToolCallSurcharge(base, prices, counts, decimal.NewFromInt(1))
	if !got.Total.Equal(base.Total) {
		t.Fatalf("zero counts: Total=%s want %s (must be unchanged)", got.Total, base.Total)
	}
}

// TestApplyToolCallSurcharge_AddsDeltaToTotal verifies that when both prices
// and counts are non-zero, Total is increased by exactly the surcharge delta.
//
// Setup: tokenTotal=0.10, web_search count=3, price=$10/1000, groupRatio=1.0
// Expected: delta = ($10/1000)*3*1 = $0.03 -> Total = 0.13
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
	// Non-total fields must be unchanged.
	assertPricingDecimal(t, "CacheCreationCost", got.CacheCreationCost, "0.03")
	assertPricingDecimal(t, "CacheReadCost", got.CacheReadCost, "0.02")
}

// TestApplyToolCallSurcharge_GroupRatioScalesSurcharge verifies that groupRatio
// is applied to the surcharge (mirroring cache_override + resolver GroupRatio
// semantics).
//
// Setup: web_search count=3, price=$10/1000, groupRatio=2.0
// Surcharge = ($10/1000)*3*2.0 = $0.06 -> Total = 0.10 + 0.06 = 0.16
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

// TestResolve_WithToolCallSurcharge_TotalEqualsTokenPlusSurcharge exercises
// the full Resolve path via a helper that applies the surcharge step.
// This proves the end-to-end integration: tokenTotal + surcharge.
//
// Token billing: 10 input tokens * 1000 micro_usd/token = 10000 micros
//
//	= 10000 / 1_000_000 = 0.01 USD (flat, groupRatio=1)
//
// Surcharge: web_search count=3, $10/1000, groupRatio=1 -> 0.03
// Total expected: 0.04
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
	// Apply the surcharge step (mirrors chat_completions_pricing usage).
	res = ApplyToolCallSurcharge(res, prices, usage.ToolCallCounts, flat.GroupRatio)

	assertPricingDecimal(t, "Total", res.Total, "0.04")
	// Mutation guard: token-only total is 0.01, not 0.04.
	if res.Total.Equal(decimal.RequireFromString("0.01")) {
		t.Fatal("surcharge was not added: Total equals token-only 0.01")
	}
}

// TestResolve_WithEmptyToolPrices_TotalUnchanged proves the default-zero path:
// empty ToolPrices -> Total == tokenTotal (no regression for existing billing).
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
	emptyPrices := toolpricing.ToolPrices{} // default: no tool prices configured

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

// TestResolve_WithZeroToolCallCounts_TotalUnchanged proves the default-zero
// path when ToolCallCounts is zero-valued (default struct).
func TestResolve_WithZeroToolCallCounts_TotalUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"input_micro_usd":1000}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	usage := Usage{InputTokens: 10} // ToolCallCounts zero-valued by default
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
