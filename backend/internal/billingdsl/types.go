package billingdsl

import "github.com/shopspring/decimal"

// TierSpec represents one tier breakpoint. UpToTokens == nil means the tier is
// the unbounded catch-all tier and must be the final tier in a bucket.
type TierSpec struct {
	UpToTokens   *int64
	RateMicroUSD decimal.Decimal
}

// BucketSpec holds the ordered tier slice for one token bucket.
type BucketSpec []TierSpec

// ExpressionSpec is the parsed DSL for all supported token buckets.
type ExpressionSpec struct {
	Input         BucketSpec
	Output        BucketSpec
	CacheCreation BucketSpec
	CacheRead     BucketSpec
}

// EvalInput carries token counts for one pricing call.
type EvalInput struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// FlatRateFallback carries already-selected flat rates from the caller. Rates
// are in micro-USD per token and Multiplier defaults to 1 when omitted.
type FlatRateFallback struct {
	Input         decimal.Decimal
	Output        decimal.Decimal
	CacheCreation decimal.Decimal
	CacheRead     decimal.Decimal
	Multiplier    decimal.Decimal

	HasInput         bool
	HasOutput        bool
	HasCacheCreation bool
	HasCacheRead     bool
}

// EvalResult carries the per-bucket and total USD cost.
type EvalResult struct {
	Total             decimal.Decimal
	InputCost         decimal.Decimal
	OutputCost        decimal.Decimal
	CacheCreationCost decimal.Decimal
	CacheReadCost     decimal.Decimal
}
