package billingdsl

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestEvaluate_FlatFallback(t *testing.T) {
	got, err := Evaluate(ExpressionSpec{}, EvalInput{
		InputTokens:  100,
		OutputTokens: 50,
	}, FlatRateFallback{
		Input:      decimal.RequireFromString("10"),
		Output:     decimal.RequireFromString("20"),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
		HasOutput:  true,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.002")
}

func TestEvaluate_FirstTierOnly(t *testing.T) {
	spec := ExpressionSpec{
		Input: BucketSpec{
			tier(upTo(100), "2"),
			tier(nil, "5"),
		},
	}
	got, err := Evaluate(spec, EvalInput{InputTokens: 40}, FlatRateFallback{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.00008")
}

func TestEvaluate_CrossesTierBoundary(t *testing.T) {
	spec := ExpressionSpec{
		Input: BucketSpec{
			tier(upTo(100), "2"),
			tier(nil, "5"),
		},
	}
	got, err := Evaluate(spec, EvalInput{InputTokens: 150}, FlatRateFallback{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.00045")
}

func TestEvaluate_LastTierUnbounded(t *testing.T) {
	spec := ExpressionSpec{
		Input: BucketSpec{
			tier(upTo(100), "2"),
			tier(nil, "5"),
		},
	}
	got, err := Evaluate(spec, EvalInput{InputTokens: 250}, FlatRateFallback{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.00095")
}

func TestEvaluate_NegativeRateFails(t *testing.T) {
	_, err := Evaluate(ExpressionSpec{
		Input: BucketSpec{tier(nil, "-1")},
	}, EvalInput{InputTokens: 1}, FlatRateFallback{})
	if !errors.Is(err, ErrPricingUnavailable) {
		t.Fatalf("Evaluate() error = %v, want ErrPricingUnavailable", err)
	}
}

func TestEvaluate_MissingOutputTier(t *testing.T) {
	_, err := Evaluate(ExpressionSpec{
		Input: BucketSpec{tier(nil, "1")},
	}, EvalInput{InputTokens: 1, OutputTokens: 1}, FlatRateFallback{})
	if !errors.Is(err, ErrPricingUnavailable) {
		t.Fatalf("Evaluate() error = %v, want ErrPricingUnavailable", err)
	}
}

func TestEvaluate_MissingOutputTierAndFlatRateFallbackAbsent(t *testing.T) {
	_, err := Evaluate(ExpressionSpec{}, EvalInput{OutputTokens: 4}, FlatRateFallback{})
	if !errors.Is(err, ErrPricingUnavailable) {
		t.Fatalf("Evaluate() error = %v, want ErrPricingUnavailable", err)
	}
}

func TestEvaluate_DecimalPrecision(t *testing.T) {
	spec := ExpressionSpec{
		Input: BucketSpec{tier(nil, "1")},
	}
	got, err := Evaluate(spec, EvalInput{InputTokens: 9007199254740993}, FlatRateFallback{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "9007199254.740993")
}

func TestEvaluate_PerBucketCosts(t *testing.T) {
	spec := ExpressionSpec{
		Input:         BucketSpec{tier(nil, "1")},
		Output:        BucketSpec{tier(nil, "2")},
		CacheCreation: BucketSpec{tier(nil, "3")},
		CacheRead:     BucketSpec{tier(nil, "4")},
	}
	got, err := Evaluate(spec, EvalInput{
		InputTokens:         10,
		OutputTokens:        10,
		CacheCreationTokens: 10,
		CacheReadTokens:     10,
	}, FlatRateFallback{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.0001")
	assertDecimalEqual(t, "CacheCreationCost", got.CacheCreationCost, "0.00003")
	assertDecimalEqual(t, "CacheReadCost", got.CacheReadCost, "0.00004")
}

func TestEvaluate_AppliesFallbackMultiplier(t *testing.T) {
	got, err := Evaluate(ExpressionSpec{}, EvalInput{InputTokens: 10}, FlatRateFallback{
		Input:      decimal.RequireFromString("100"),
		Multiplier: decimal.RequireFromString("2.5"),
		HasInput:   true,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertDecimalEqual(t, "Total", got.Total, "0.0025")
}

func upTo(v int64) *int64 {
	return &v
}

func tier(upToTokens *int64, rate string) TierSpec {
	return TierSpec{
		UpToTokens:   upToTokens,
		RateMicroUSD: decimal.RequireFromString(rate),
	}
}

func assertDecimalEqual(t *testing.T, label string, got decimal.Decimal, want string) {
	t.Helper()
	wantDecimal := decimal.RequireFromString(want)
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s = %s, want %s", label, got.String(), wantDecimal.String())
	}
}
