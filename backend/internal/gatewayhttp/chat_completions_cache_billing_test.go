package gatewayhttp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

func TestDeepSeekCacheReadMigrationUsesOneTenthInputRate(t *testing.T) {
	baseModels := migrationJSONMap(t, "0131_domestic_model_pricing.up.sql", "models")
	cacheRates := migrationJSONMap(t, "0179_deepseek_cache_read_pricing.up.sql", "cache_rates")

	for _, model := range []string{"deepseek-chat", "deepseek-coder", "deepseek-reasoner"} {
		t.Run(model, func(t *testing.T) {
			base, ok := baseModels[model]
			if !ok {
				t.Fatalf("0131 缺少模型 %q", model)
			}
			patch, ok := cacheRates[model]
			if !ok {
				t.Fatalf("0179 缺少模型 %q 的缓存费率", model)
			}

			baseVector, err := parseRateVector(base)
			if err != nil {
				t.Fatalf("解析 0131 模型费率失败：%v", err)
			}
			patchVector, err := parseRateVector(patch)
			if err != nil {
				t.Fatalf("解析 0179 缓存费率失败：%v", err)
			}
			if !baseVector.HasInput || !patchVector.HasCacheRead {
				t.Fatalf("费率维度不完整：base=%+v patch=%+v", baseVector, patchVector)
			}
			if !patchVector.CacheRead.Mul(decimal.NewFromInt(10)).Equal(baseVector.Input) {
				t.Fatalf("cache_read=%s，期望 input=%s 的 1/10", patchVector.CacheRead, baseVector.Input)
			}
		})
	}
}

func TestDeepSeekCacheReadMigrationDownOnlyRemovesAddedField(t *testing.T) {
	raw, err := os.ReadFile("../../sql/migrations/0179_deepseek_cache_read_pricing.down.sql")
	if err != nil {
		t.Fatalf("读取 0179 down 迁移失败：%v", err)
	}
	sql := string(raw)
	if got := strings.Count(sql, "- 'cache_read_micro_usd'"); got != 3 {
		t.Fatalf("down 删除 cache_read_micro_usd 次数=%d，期望 3", got)
	}
	for _, model := range []string{"deepseek-chat", "deepseek-coder", "deepseek-reasoner"} {
		if strings.Contains(sql, "- '"+model+"'") {
			t.Fatalf("down 不得删除整个模型对象 %q", model)
		}
		if !strings.Contains(sql, "'{models,"+model+"}'") {
			t.Fatalf("down 缺少模型 %q 的外科式路径", model)
		}
	}
}

func TestCompletionCost_DeepSeekCacheHitUsesMigratedPrice(t *testing.T) {
	modelRate := mergedDeepSeekModelRate(t, "deepseek-chat")
	pricingData, err := json.Marshal(map[string]any{
		"models": map[string]json.RawMessage{"deepseek-chat": modelRate},
	})
	if err != nil {
		t.Fatalf("构造真实价表失败：%v", err)
	}

	// prompt=10000，其中命中 2000；output=1000。
	// 未命中 8000*0.28 + 命中 2000*0.028 + 输出 1000*0.42 = 2716 micro-USD。
	cost, err := newCacheBillingExec("deepseek_chat", "deepseek-chat", string(pricingData)).
		completionCost(completionUsageForCost{
			InputTokens:     10_000,
			OutputTokens:    1_000,
			CacheReadTokens: 2_000,
		})
	if err != nil {
		t.Fatalf("DeepSeek 命中计费失败：%v", err)
	}

	assertDecimalEqual(t, "Total", cost.Total, decimal.RequireFromString("0.002716"))
	assertDecimalEqual(t, "CacheReadCost", cost.CacheReadCost, decimal.RequireFromString("0.000056"))
	assertDecimalEqual(t, "Total micro-USD", cost.Total.Mul(decimal.NewFromInt(1_000_000)), decimal.NewFromInt(2716))
	assertDecimalEqual(t, "CacheRead micro-USD", cost.CacheReadCost.Mul(decimal.NewFromInt(1_000_000)), decimal.NewFromInt(56))

	// 若缓存 token 仍按 input 全价，结果会是 3220 micro-USD，必须与正确结果可判别。
	if cost.Total.Mul(decimal.NewFromInt(1_000_000)).Equal(decimal.NewFromInt(3220)) {
		t.Fatal("命中 token 仍按普通 input 费率计价")
	}
}

func mergedDeepSeekModelRate(t *testing.T, model string) json.RawMessage {
	t.Helper()
	baseModels := migrationJSONMap(t, "0131_domestic_model_pricing.up.sql", "models")
	cacheRates := migrationJSONMap(t, "0179_deepseek_cache_read_pricing.up.sql", "cache_rates")
	base, ok := baseModels[model]
	if !ok {
		t.Fatalf("0131 缺少模型 %q", model)
	}
	patch, ok := cacheRates[model]
	if !ok {
		t.Fatalf("0179 缺少模型 %q", model)
	}

	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		t.Fatalf("解析 0131 模型 %q 失败：%v", model, err)
	}
	var additions map[string]json.RawMessage
	if err := json.Unmarshal(patch, &additions); err != nil {
		t.Fatalf("解析 0179 模型 %q 失败：%v", model, err)
	}
	for key, value := range additions {
		merged[key] = value
	}
	out, err := json.Marshal(merged)
	if err != nil {
		t.Fatalf("合并模型 %q 费率失败：%v", model, err)
	}
	return out
}

func migrationJSONMap(t *testing.T, filename, tag string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../../sql/migrations/" + filename)
	if err != nil {
		t.Fatalf("读取迁移 %s 失败：%v", filename, err)
	}
	marker := "$" + tag + "$"
	parts := strings.Split(string(raw), marker)
	if len(parts) < 3 {
		t.Fatalf("迁移 %s 缺少成对 JSON 标记 %s", filename, marker)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(parts[1]), &values); err != nil {
		t.Fatalf("迁移 %s 的 JSON 无效：%v", filename, err)
	}
	return values
}
