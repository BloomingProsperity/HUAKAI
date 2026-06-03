package billingdsl

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParse_AcceptsValidExpression(t *testing.T) {
	spec, err := ParsePricingExpression(json.RawMessage(`{
		"input": [
			{"up_to_tokens": 100, "rate_micro_usd": "2.5"},
			{"up_to_tokens": null, "rate_micro_usd": "5"}
		],
		"output": [
			{"up_to_tokens": null, "rate_micro_usd": "10"}
		],
		"cache_creation": [
			{"up_to_tokens": null, "rate_micro_usd": "7"}
		],
		"cache_read": [
			{"up_to_tokens": null, "rate_micro_usd": "1"}
		]
	}`))
	if err != nil {
		t.Fatalf("ParsePricingExpression() error = %v", err)
	}
	if len(spec.Input) != 2 || spec.Input[0].UpToTokens == nil || *spec.Input[0].UpToTokens != 100 {
		t.Fatalf("input tiers not parsed: %+v", spec.Input)
	}
	if !spec.Input[0].RateMicroUSD.Equal(decimal.RequireFromString("2.5")) {
		t.Fatalf("input tier rate = %s", spec.Input[0].RateMicroUSD)
	}
	if spec.Input[1].UpToTokens != nil {
		t.Fatalf("unbounded tier should have nil upper bound")
	}
}

func TestParse_RejectsOverlappingBreakpoints(t *testing.T) {
	_, err := ParsePricingExpression(json.RawMessage(`{
		"input": [
			{"up_to_tokens": 100, "rate_micro_usd": "2"},
			{"up_to_tokens": 100, "rate_micro_usd": "3"}
		]
	}`))
	if err == nil {
		t.Fatal("expected duplicate breakpoint to fail")
	}
}

func TestParse_RejectsUnsortedBreakpoints(t *testing.T) {
	_, err := ParsePricingExpression(json.RawMessage(`{
		"input": [
			{"up_to_tokens": 200, "rate_micro_usd": "2"},
			{"up_to_tokens": 100, "rate_micro_usd": "3"}
		]
	}`))
	if err == nil {
		t.Fatal("expected unsorted breakpoints to fail")
	}
}

func TestParse_RejectsMultipleUnbounded(t *testing.T) {
	_, err := ParsePricingExpression(json.RawMessage(`{
		"output": [
			{"up_to_tokens": null, "rate_micro_usd": "2"},
			{"up_to_tokens": null, "rate_micro_usd": "3"}
		]
	}`))
	if err == nil {
		t.Fatal("expected multiple unbounded tiers to fail")
	}
}

func TestParse_RejectsUnboundedBeforeLastTier(t *testing.T) {
	_, err := ParsePricingExpression(json.RawMessage(`{
		"output": [
			{"up_to_tokens": null, "rate_micro_usd": "2"},
			{"up_to_tokens": 200, "rate_micro_usd": "3"}
		]
	}`))
	if err == nil {
		t.Fatal("expected unbounded non-last tier to fail")
	}
}

func TestParse_RejectsNegativeRate(t *testing.T) {
	_, err := ParsePricingExpression(json.RawMessage(`{
		"cache_read": [
			{"up_to_tokens": null, "rate_micro_usd": "-0.01"}
		]
	}`))
	if err == nil {
		t.Fatal("expected negative rate to fail")
	}
}
