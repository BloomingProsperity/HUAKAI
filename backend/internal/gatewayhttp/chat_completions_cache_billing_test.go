package gatewayhttp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func newCacheBillingExec(family, model, pricing string) *chatExecution {
	return &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(pricing),
			}},
			BillingPolicyVersion: "test-policy",
		},
		ident:    auth.Identity{TenantID: 7},
		req:      chatRequest{Model: model},
		resolved: registry.Resolved{ProtocolFamily: family},
	}
}

// Fix 1 (cache double-count): cache-inclusive families subtract cached tokens from
// the input bucket so a cached token is billed once (at the cache rate), not twice
// (input rate + cache rate). Anthropic-family input_tokens already excludes cache.
func TestCompletionCost_CacheInclusiveFamilyExcludesCachedFromInputBucket(t *testing.T) {
	const pricing = `{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000,"cache_read_micro_usd":100}}}`
	usage := completionUsageForCost{InputTokens: 1000, CacheReadTokens: 200}

	openaiCost, err := newCacheBillingExec("openai_chat", "gpt-4o", pricing).completionCost(usage)
	if err != nil {
		t.Fatalf("openai completionCost: %v", err)
	}
	anthropicCost, err := newCacheBillingExec("anthropic_messages", "gpt-4o", pricing).completionCost(usage)
	if err != nil {
		t.Fatalf("anthropic completionCost: %v", err)
	}

	// Anthropic input_tokens already excludes the 200 cached -> input bucket bills 1000.
	// (1000*1000 + 200*100)/1e6 = 1.02
	assertDecimalEqual(t, "anthropic Total", anthropicCost.Total, decimal.RequireFromString("1.02"))
	// OpenAI prompt_tokens includes the 200 cached -> input bucket must bill only 800.
	// (800*1000 + 200*100)/1e6 = 0.82
	assertDecimalEqual(t, "openai Total", openaiCost.Total, decimal.RequireFromString("0.82"))
	// Mutation guard: dropping billingUsageForCacheConvention makes these equal.
	if !openaiCost.Total.LessThan(anthropicCost.Total) {
		t.Fatalf("cache-inclusive family must bill the cached tokens once: openai=%s anthropic=%s",
			openaiCost.Total, anthropicCost.Total)
	}
}

// Fix 2 (fail-closed availability) through the primary FlatCost path: a priced model
// that omits a cache-read rate bills cached tokens at the input rate instead of
// returning pricing_unavailable (503). Combined with Fix 1 a cache-inclusive provider
// nets the full prompt at the input rate -- no double count, no 503.
func TestCompletionCost_UnpricedCacheReadBillsAtInputRateNotFailClosed(t *testing.T) {
	const pricing = `{"models":{"glm-4.6":{"input_micro_usd":600,"output_micro_usd":2200}}}`
	// glm-4.6 (cache-inclusive) reports prompt_tokens=1000 incl 300 cached, no cache rate.
	cost, err := newCacheBillingExec("glm_chat", "glm-4.6", pricing).
		completionCost(completionUsageForCost{InputTokens: 1000, CacheReadTokens: 300})
	if err != nil {
		t.Fatalf("cache hit on a model without a cache rate must not fail closed: %v", err)
	}
	// (700 non-cached + 300 cached) * 600 / 1e6 = 0.6
	assertDecimalEqual(t, "Total", cost.Total, decimal.RequireFromString("0.6"))
}

// Fix 2 at the rate-vector layer: parseRateVector defaults the cache-read rate to the
// input rate when unpriced, so price() never returns a missing-rate error.
func TestParseRateVector_DefaultsCacheReadRateToInputWhenUnpriced(t *testing.T) {
	rates, err := parseRateVector(json.RawMessage(`{"input_micro_usd":1000,"output_micro_usd":2000}`))
	if err != nil {
		t.Fatalf("parseRateVector: %v", err)
	}
	got, err := rates.price(completionUsageForCost{CacheReadTokens: 200})
	if err != nil {
		t.Fatalf("cached tokens on an input-priced model must not fail closed: %v", err)
	}
	// 200 * 1000 / 1e6 = 0.2 (cached billed at the input rate)
	assertDecimalEqual(t, "CacheReadCost", got.CacheReadCost, decimal.RequireFromString("0.2"))
	assertDecimalEqual(t, "Total", got.Total, decimal.RequireFromString("0.2"))
}
