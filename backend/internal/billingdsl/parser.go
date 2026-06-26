package billingdsl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type rawExpressionSpec struct {
	Input         []rawTierSpec `json:"input"`
	Output        []rawTierSpec `json:"output"`
	CacheCreation []rawTierSpec `json:"cache_creation"`
	CacheRead     []rawTierSpec `json:"cache_read"`
}

type rawTierSpec struct {
	UpToTokens   *int64          `json:"up_to_tokens"`
	RateMicroUSD json.RawMessage `json:"rate_micro_usd"`
}

// ParsePricingExpression 把一个 tier_rules JSONB blob 解析成 ExpressionSpec。
func ParsePricingExpression(raw json.RawMessage) (ExpressionSpec, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return ExpressionSpec{}, pricingUnavailable("tier_rules empty")
	}
	var parsed rawExpressionSpec
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ExpressionSpec{}, pricingUnavailable(fmt.Sprintf("tier_rules invalid: %v", err))
	}
	var out ExpressionSpec
	var err error
	if out.Input, err = parseBucket("input", parsed.Input); err != nil {
		return ExpressionSpec{}, err
	}
	if out.Output, err = parseBucket("output", parsed.Output); err != nil {
		return ExpressionSpec{}, err
	}
	if out.CacheCreation, err = parseBucket("cache_creation", parsed.CacheCreation); err != nil {
		return ExpressionSpec{}, err
	}
	if out.CacheRead, err = parseBucket("cache_read", parsed.CacheRead); err != nil {
		return ExpressionSpec{}, err
	}
	return out, nil
}

func parseBucket(bucket string, raw []rawTierSpec) (BucketSpec, error) {
	out := make(BucketSpec, 0, len(raw))
	for idx, tier := range raw {
		if len(tier.RateMicroUSD) == 0 {
			return nil, pricingUnavailable(fmt.Sprintf("%s tier %d rate missing", bucket, idx))
		}
		rate, err := parseDecimal(tier.RateMicroUSD)
		if err != nil {
			return nil, pricingUnavailable(fmt.Sprintf("%s tier %d rate invalid: %v", bucket, idx, err))
		}
		out = append(out, TierSpec{UpToTokens: tier.UpToTokens, RateMicroUSD: rate})
	}
	if err := validateBucket(bucket, out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateBucket(bucket string, tiers BucketSpec) error {
	var lastBound int64
	for idx, tier := range tiers {
		if tier.RateMicroUSD.IsNegative() {
			return pricingUnavailable(fmt.Sprintf("%s tier %d rate negative", bucket, idx))
		}
		if tier.UpToTokens == nil {
			if idx != len(tiers)-1 {
				return pricingUnavailable(fmt.Sprintf("%s unbounded tier must be last", bucket))
			}
			continue
		}
		if *tier.UpToTokens <= 0 {
			return pricingUnavailable(fmt.Sprintf("%s tier %d upper bound must be positive", bucket, idx))
		}
		if idx > 0 && *tier.UpToTokens <= lastBound {
			return pricingUnavailable(fmt.Sprintf("%s tier breakpoints must be strictly increasing", bucket))
		}
		lastBound = *tier.UpToTokens
	}
	return nil
}

func parseDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return decimal.NewFromString(strings.TrimSpace(text))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}
