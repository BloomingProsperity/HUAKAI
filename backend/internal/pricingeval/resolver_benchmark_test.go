package pricingeval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

var benchmarkResolveResult Result

func BenchmarkResolveTieredPricing(b *testing.B) {
	raw := json.RawMessage(`{
		"pricing_model":"tiered",
		"input_micro_usd":1000,
		"output_micro_usd":2000,
		"cache_read_micro_usd":100,
		"input":[
			{"up_to_tokens":1000,"rate_micro_usd":"100"},
			{"up_to_tokens":null,"rate_micro_usd":"300"}
		],
		"output":[
			{"up_to_tokens":500,"rate_micro_usd":"400"},
			{"up_to_tokens":null,"rate_micro_usd":"900"}
		]
	}`)
	usage := Usage{
		InputTokens:     4096,
		OutputTokens:    768,
		CacheReadTokens: 2048,
	}
	fallback := FlatRateFallback{
		Input:        decimal.NewFromInt(1000),
		Output:       decimal.NewFromInt(2000),
		CacheRead:    decimal.NewFromInt(100),
		Multiplier:   decimal.NewFromInt(1),
		HasInput:     true,
		HasOutput:    true,
		HasCacheRead: true,
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := Resolve(ctx, raw, usage, fallback, "bench-v1")
		if err != nil {
			b.Fatalf("Resolve: %v", err)
		}
		benchmarkResolveResult = got
	}
}
