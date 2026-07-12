package billingdsl

import "github.com/shopspring/decimal"

// TierSpec 表示一个分层断点。UpToTokens == nil 表示该层是无上界的兜底层，
// 且必须是某个 bucket 中的最后一层。
type TierSpec struct {
	UpToTokens   *int64
	RateMicroUSD decimal.Decimal
}

// BucketSpec 保存某个 token bucket 的有序分层切片。
type BucketSpec []TierSpec

// ExpressionSpec 是所有受支持的 token bucket 解析后的 DSL。
type ExpressionSpec struct {
	Input         BucketSpec
	Output        BucketSpec
	CacheCreation BucketSpec
	CacheRead     BucketSpec
}

// EvalInput 携带单次定价调用的 token 计数。
type EvalInput struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// FlatRateFallback 携带调用方已选定的统一费率。费率单位为每 token 的
// micro-USD，Multiplier 省略时默认为 1。
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

// EvalResult 携带各 bucket 的成本以及总的 USD 成本。
type EvalResult struct {
	Total             decimal.Decimal
	InputCost         decimal.Decimal
	OutputCost        decimal.Decimal
	CacheCreationCost decimal.Decimal
	CacheReadCost     decimal.Decimal
}
