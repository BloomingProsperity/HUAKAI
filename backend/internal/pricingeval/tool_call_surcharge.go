package pricingeval

import (
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// ApplyToolCallSurcharge adds the built-in tool-call surcharge to an existing
// billing Result. It is an additive Total-adjuster that mirrors the shape of
// ApplyCacheCostOverride: a strict no-op when unconfigured.
//
// Default-zero safety invariants (all must hold for the surcharge to be non-zero):
//   - Usage.ToolCallCounts must be non-zero (at least one tool was called)
//   - prices returned by the Table lookup must be non-zero (tenant opted in)
//
// When either condition is false the function returns res unmodified —
// Total is byte-identical to the token-only result produced by Resolve().
//
// groupRatio is passed through to toolpricing.Surcharge; zero is treated as 1.0
// (matching pricingeval.pricingGroupRatio semantics).
func ApplyToolCallSurcharge(res Result, prices toolpricing.ToolPrices, counts toolpricing.ToolCallCounts, groupRatio decimal.Decimal) Result {
	if prices.IsZero() || counts.IsZero() {
		return res
	}
	delta := toolpricing.Surcharge(prices, counts, groupRatio)
	if delta.IsZero() {
		return res
	}
	res.Total = res.Total.Add(delta)
	return res
}
