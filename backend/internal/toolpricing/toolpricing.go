package toolpricing

import "github.com/shopspring/decimal"

// ToolCallCounts holds the number of server-side built-in tool calls made in a
// single completion. All fields default to zero, which means no surcharge.
//
// Upstreams (OpenAI / Anthropic) charge a flat per-CALL fee for built-in tools
// (web_search, file_search, image_generation) independent of tokens. This struct
// carries the extracted call counts so the surcharge step can compute the delta.
type ToolCallCounts struct {
	WebSearch       int64
	FileSearch      int64
	ImageGeneration int64
}

// IsZero reports whether all call counts are zero (no tool calls = no surcharge).
func (c ToolCallCounts) IsZero() bool {
	return c.WebSearch == 0 && c.FileSearch == 0 && c.ImageGeneration == 0
}

// ToolPrices holds per-tool USD prices per 1000 calls for a single
// (tenant, model) configuration entry. A zero price means the tool is unpriced
// (no surcharge for that tool).
type ToolPrices struct {
	// WebSearchPer1000 is the USD charge per 1000 web_search tool calls.
	WebSearchPer1000 decimal.Decimal
	// FileSearchPer1000 is the USD charge per 1000 file_search tool calls.
	FileSearchPer1000 decimal.Decimal
	// ImageGenerationPer1000 is the USD charge per 1000 image_generation calls.
	ImageGenerationPer1000 decimal.Decimal
}

// IsZero reports whether all prices are zero (no surcharge will be added).
func (p ToolPrices) IsZero() bool {
	return p.WebSearchPer1000.IsZero() &&
		p.FileSearchPer1000.IsZero() &&
		p.ImageGenerationPer1000.IsZero()
}

// Table maps (tenantID, modelID) to ToolPrices. An empty Table (nil or no
// entries) returns zero prices for every lookup, guaranteeing the default
// behavior is zero surcharge.
type Table map[tableKey]ToolPrices

type tableKey struct {
	TenantID int64
	ModelID  string
}

// Lookup returns the ToolPrices for the given tenant and model. If no entry is
// found, zero prices are returned (safe default: no surcharge).
func (t Table) Lookup(tenantID int64, modelID string) ToolPrices {
	if t == nil {
		return ToolPrices{}
	}
	return t[tableKey{TenantID: tenantID, ModelID: modelID}]
}

// Set stores prices for the given tenant and model. Used for testing and for
// loading configuration.
func (t Table) Set(tenantID int64, modelID string, prices ToolPrices) {
	t[tableKey{TenantID: tenantID, ModelID: modelID}] = prices
}

var per1000 = decimal.NewFromInt(1000)

// Surcharge computes the total tool-call surcharge in USD.
//
// Formula per tool: price_per_1000 / 1000 * count * groupRatio
//
// Invariants that guarantee the default-zero safety property:
//   - zero counts  -> zero surcharge (no calls = no charge)
//   - zero prices  -> zero surcharge (unconfigured = no charge)
//   - empty Table  -> zero surcharge (no config = no charge)
//
// groupRatio mirrors the pricingeval GroupRatio semantics: zero is treated as
// 1.0 (identity), matching pricingeval.pricingGroupRatio behaviour.
func Surcharge(prices ToolPrices, counts ToolCallCounts, groupRatio decimal.Decimal) decimal.Decimal {
	if prices.IsZero() || counts.IsZero() {
		return decimal.Zero
	}
	ratio := effectiveGroupRatio(groupRatio)
	total := decimal.Zero
	total = total.Add(toolFee(prices.WebSearchPer1000, counts.WebSearch))
	total = total.Add(toolFee(prices.FileSearchPer1000, counts.FileSearch))
	total = total.Add(toolFee(prices.ImageGenerationPer1000, counts.ImageGeneration))
	return total.Mul(ratio)
}

// toolFee computes price_per_1000 / 1000 * count for a single tool.
// Returns zero when count <= 0 or price is zero.
func toolFee(pricePer1000 decimal.Decimal, count int64) decimal.Decimal {
	if count <= 0 || pricePer1000.IsZero() {
		return decimal.Zero
	}
	return pricePer1000.Div(per1000).Mul(decimal.NewFromInt(count))
}

// effectiveGroupRatio returns a safe group ratio: zero -> 1 (identity).
func effectiveGroupRatio(ratio decimal.Decimal) decimal.Decimal {
	if ratio.IsZero() {
		return decimal.NewFromInt(1)
	}
	return ratio
}

// DefaultToolPrices returns the platform-level default ToolPrices for the
// new-api / official upstream billing schedule (USD per 1000 calls, 1:1 with
// HUAKAI billing — no QuotaPerUnit conversion needed):
//
//   - WebSearchPer1000:  $10.00  (new-api operation_setting web_search=10.0)
//   - FileSearchPer1000: $ 2.50  (new-api operation_setting file_search=2.5)
//   - ImageGenerationPer1000: $0  (deferred to Stage D — upstream image-gen
//     pricing is model/size-dependent and requires a separate price table)
//
// Callers that want tenant-level overrides should use Table.Lookup; this
// function is the zero-config fallback constant.
func DefaultToolPrices() ToolPrices {
	return ToolPrices{
		WebSearchPer1000:       decimal.NewFromFloat(10.0),
		FileSearchPer1000:      decimal.NewFromFloat(2.5),
		ImageGenerationPer1000: decimal.Zero, // Stage D: model/size-based pricing deferred
	}
}
