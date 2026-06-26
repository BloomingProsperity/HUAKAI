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

// 修复 1(缓存重复计数):cache-inclusive 族会从 input 桶里扣掉已缓存 token，
// 使一个已缓存 token 只按 cache 费率计一次，而非两次(input 费率 + cache 费率)。
// anthropic 族的 input_tokens 本身就已不含缓存。
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

	// anthropic 的 input_tokens 本就不含那 200 个已缓存 token -> input 桶计 1000。
	// (1000*1000 + 200*100)/1e6 = 1.02
	assertDecimalEqual(t, "anthropic Total", anthropicCost.Total, decimal.RequireFromString("1.02"))
	// OpenAI 的 prompt_tokens 含那 200 个已缓存 token -> input 桶只应计 800。
	// (800*1000 + 200*100)/1e6 = 0.82
	assertDecimalEqual(t, "openai Total", openaiCost.Total, decimal.RequireFromString("0.82"))
	// 变异:守卫:去掉 billingUsageForCacheConvention 会让二者相等。
	if !openaiCost.Total.LessThan(anthropicCost.Total) {
		t.Fatalf("cache-inclusive family must bill the cached tokens once: openai=%s anthropic=%s",
			openaiCost.Total, anthropicCost.Total)
	}
}

// 修复 2(fail-closed 可用性)走主 FlatCost 路径:一个已定价但未给出 cache-read
// 费率的模型，会按 input 费率对已缓存 token 计费，而不是返回 pricing_unavailable
// (503)。配合修复 1,cache-inclusive 提供方整提示词净按 input 费率计 —— 不重复计数,
// 也不报 503。
func TestCompletionCost_UnpricedCacheReadBillsAtInputRateNotFailClosed(t *testing.T) {
	const pricing = `{"models":{"glm-4.6":{"input_micro_usd":600,"output_micro_usd":2200}}}`
	// glm-4.6(cache-inclusive)报告 prompt_tokens=1000(含 300 已缓存),无 cache 费率。
	cost, err := newCacheBillingExec("glm_chat", "glm-4.6", pricing).
		completionCost(completionUsageForCost{InputTokens: 1000, CacheReadTokens: 300})
	if err != nil {
		t.Fatalf("cache hit on a model without a cache rate must not fail closed: %v", err)
	}
	// (700 未缓存 + 300 已缓存) * 600 / 1e6 = 0.6
	assertDecimalEqual(t, "Total", cost.Total, decimal.RequireFromString("0.6"))
}

// 修复 2 在 rate-vector 层:未定价时 parseRateVector 把 cache-read 费率默认取为
// input 费率，使 price() 永不返回缺失费率错误。
func TestParseRateVector_DefaultsCacheReadRateToInputWhenUnpriced(t *testing.T) {
	rates, err := parseRateVector(json.RawMessage(`{"input_micro_usd":1000,"output_micro_usd":2000}`))
	if err != nil {
		t.Fatalf("parseRateVector: %v", err)
	}
	got, err := rates.price(completionUsageForCost{CacheReadTokens: 200})
	if err != nil {
		t.Fatalf("cached tokens on an input-priced model must not fail closed: %v", err)
	}
	// 200 * 1000 / 1e6 = 0.2(已缓存 token 按 input 费率计费)
	assertDecimalEqual(t, "CacheReadCost", got.CacheReadCost, decimal.RequireFromString("0.2"))
	assertDecimalEqual(t, "Total", got.Total, decimal.RequireFromString("0.2"))
}
