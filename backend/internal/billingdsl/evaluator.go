package billingdsl

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// ErrPricingUnavailable 在分层表达式无法为非零用量产出金额安全的成本时
// 返回。
var ErrPricingUnavailable = errors.New("billingdsl: pricing unavailable")

var microUSDDivisor = decimal.NewFromInt(1_000_000)

// Evaluate 使用分层表达式计算单次请求的 USD 成本。
func Evaluate(spec ExpressionSpec, input EvalInput, fallback FlatRateFallback) (EvalResult, error) {
	multiplier, err := normalizedMultiplier(fallback.Multiplier)
	if err != nil {
		return EvalResult{}, err
	}
	inputMicros, err := bucketMicros("input", input.InputTokens, spec.Input, fallback.Input, fallback.HasInput)
	if err != nil {
		return EvalResult{}, err
	}
	outputMicros, err := bucketMicros("output", input.OutputTokens, spec.Output, fallback.Output, fallback.HasOutput)
	if err != nil {
		return EvalResult{}, err
	}
	cacheCreationMicros, err := bucketMicros("cache_creation", input.CacheCreationTokens, spec.CacheCreation, fallback.CacheCreation, fallback.HasCacheCreation)
	if err != nil {
		return EvalResult{}, err
	}
	cacheReadMicros, err := bucketMicros("cache_read", input.CacheReadTokens, spec.CacheRead, fallback.CacheRead, fallback.HasCacheRead)
	if err != nil {
		return EvalResult{}, err
	}
	inputCost := scaleMicros(inputMicros, multiplier)
	outputCost := scaleMicros(outputMicros, multiplier)
	cacheCreationCost := scaleMicros(cacheCreationMicros, multiplier)
	cacheReadCost := scaleMicros(cacheReadMicros, multiplier)
	return EvalResult{
		Total:             inputCost.Add(outputCost).Add(cacheCreationCost).Add(cacheReadCost),
		InputCost:         inputCost,
		OutputCost:        outputCost,
		CacheCreationCost: cacheCreationCost,
		CacheReadCost:     cacheReadCost,
	}, nil
}

func bucketMicros(bucket string, tokens int64, tiers BucketSpec, fallbackRate decimal.Decimal, hasFallback bool) (decimal.Decimal, error) {
	if tokens < 0 {
		return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s tokens negative", bucket))
	}
	if tokens == 0 {
		return decimal.Zero, nil
	}
	if len(tiers) == 0 {
		return flatBucketMicros(bucket, tokens, fallbackRate, hasFallback)
	}
	if err := validateBucket(bucket, tiers); err != nil {
		return decimal.Zero, err
	}
	return tieredBucketMicros(bucket, tokens, tiers)
}

func flatBucketMicros(bucket string, tokens int64, rate decimal.Decimal, hasRate bool) (decimal.Decimal, error) {
	if !hasRate {
		return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s rate missing", bucket))
	}
	if rate.IsNegative() {
		return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s rate negative", bucket))
	}
	return decimal.NewFromInt(tokens).Mul(rate), nil
}

func tieredBucketMicros(bucket string, tokens int64, tiers BucketSpec) (decimal.Decimal, error) {
	total := decimal.Zero
	remaining := tokens
	lowerBound := int64(0)
	for _, tier := range tiers {
		span := remaining
		if tier.UpToTokens != nil {
			span = minInt64(remaining, *tier.UpToTokens-lowerBound)
			lowerBound = *tier.UpToTokens
		}
		if span > 0 {
			total = total.Add(decimal.NewFromInt(span).Mul(tier.RateMicroUSD))
			remaining -= span
		}
		if remaining == 0 {
			return total, nil
		}
	}
	return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s tier coverage missing", bucket))
}

func normalizedMultiplier(multiplier decimal.Decimal) (decimal.Decimal, error) {
	if multiplier.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	if multiplier.IsNegative() {
		return decimal.Zero, pricingUnavailable("multiplier negative")
	}
	return multiplier, nil
}

func scaleMicros(micros decimal.Decimal, multiplier decimal.Decimal) decimal.Decimal {
	return micros.Mul(multiplier).Div(microUSDDivisor)
}

func pricingUnavailable(reason string) error {
	return fmt.Errorf("%w: %s", ErrPricingUnavailable, reason)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
