package pricingeval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestResolve_TieredExpressionOverridesFlatWhenUsageCrossesTier(t *testing.T) {
	raw := json.RawMessage(`{
		"pricing_model":"tiered",
		"input_micro_usd":1000,
		"input":[
			{"up_to_tokens":100,"rate_micro_usd":"100"},
			{"up_to_tokens":null,"rate_micro_usd":"300"}
		]
	}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}

	got, err := Resolve(context.Background(), raw, Usage{InputTokens: 150}, flat, "price-v7")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	flatCost, err := FlatCost(Usage{InputTokens: 150}, flat)
	if err != nil {
		t.Fatalf("FlatCost() error = %v", err)
	}
	if got.Total.Equal(flatCost.Total) {
		t.Fatalf("tiered fixture is non-discriminating: tiered total equals flat total %s", got.Total)
	}
	assertPricingDecimal(t, "Total", got.Total, "0.025")
	if got.CostSnapshot != "tiered:vprice-v7" {
		t.Fatalf("CostSnapshot=%q want tiered:vprice-v7", got.CostSnapshot)
	}
	if got.PendingReconciliation {
		t.Fatal("PendingReconciliation=true want false for valid tiered pricing")
	}
}

func TestResolve_FlatDataUsesLegacySplitCacheMath(t *testing.T) {
	raw := json.RawMessage(`{
		"cache_creation_micro_usd":1500,
		"cache_creation_5m_micro_usd":1250,
		"cache_creation_1h_micro_usd":2000,
		"model_multiplier":"1"
	}`)
	flat := FlatRateFallback{
		CacheCreation:      decimal.NewFromInt(1500),
		CacheCreation5m:    decimal.NewFromInt(1250),
		CacheCreation1h:    decimal.NewFromInt(2000),
		Multiplier:         decimal.NewFromInt(1),
		HasCacheCreation:   true,
		HasCacheCreation5m: true,
		HasCacheCreation1h: true,
	}

	got, err := Resolve(context.Background(), raw, Usage{
		CacheCreation5mTokens: 10,
		CacheCreation1hTokens: 20,
	}, flat, "price-v7")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// Mutation guard: routing this flat data through billingdsl's aggregate
	// fallback would charge 30 * 1500 = 0.045, not the legacy split 0.0525.
	assertPricingDecimal(t, "Total", got.Total, "0.0525")
	assertPricingDecimal(t, "CacheCreationCost", got.CacheCreationCost, "0.0525")
	if got.CostSnapshot != "flat" {
		t.Fatalf("CostSnapshot=%q want flat", got.CostSnapshot)
	}
}

func TestResolve_InvalidTieredExpressionFallsBackToFlatAndSignals(t *testing.T) {
	raw := json.RawMessage(`{
		"pricing_model":"tiered",
		"input_micro_usd":1000,
		"input":[
			{"up_to_tokens":200,"rate_micro_usd":"100"},
			{"up_to_tokens":100,"rate_micro_usd":"300"}
		]
	}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	before := SnapshotSignals().TieredFallbackTotal

	got, err := Resolve(context.Background(), raw, Usage{InputTokens: 150}, flat, "price-v7")
	if err != nil {
		t.Fatalf("Resolve() must fail soft to flat, got error %v", err)
	}

	assertPricingDecimal(t, "Total", got.Total, "0.15")
	if got.CostSnapshot != "flat" {
		t.Fatalf("CostSnapshot=%q want flat for charged fallback model", got.CostSnapshot)
	}
	if !got.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true for tiered fail-soft signal")
	}
	after := SnapshotSignals().TieredFallbackTotal
	if after-before != 1 {
		t.Fatalf("TieredFallbackTotal delta=%d want 1", after-before)
	}
}

func TestResolve_GroupRatioScalesFlatAndTieredTotalsOnce(t *testing.T) {
	ratio := decimal.RequireFromString("0.8")
	tests := []struct {
		name      string
		raw       json.RawMessage
		usage     Usage
		fallback  FlatRateFallback
		baseTotal string
		wantTotal string
	}{
		{
			name: "flat",
			raw:  json.RawMessage(`{"input_micro_usd":1000}`),
			usage: Usage{
				InputTokens: 10,
			},
			fallback: FlatRateFallback{
				Input:      decimal.NewFromInt(1000),
				Multiplier: decimal.NewFromInt(1),
				GroupRatio: ratio,
				HasInput:   true,
			},
			baseTotal: "0.01",
			wantTotal: "0.008",
		},
		{
			name: "tiered",
			raw: json.RawMessage(`{
				"pricing_model":"tiered",
				"input_micro_usd":1000,
				"input":[
					{"up_to_tokens":100,"rate_micro_usd":"100"},
					{"up_to_tokens":null,"rate_micro_usd":"300"}
				]
			}`),
			usage: Usage{
				InputTokens: 150,
			},
			fallback: FlatRateFallback{
				Input:      decimal.NewFromInt(1000),
				Multiplier: decimal.NewFromInt(1),
				GroupRatio: ratio,
				HasInput:   true,
			},
			baseTotal: "0.025",
			wantTotal: "0.02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(context.Background(), tt.raw, tt.usage, tt.fallback, "price-v7")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			assertPricingDecimal(t, "Total", got.Total, tt.wantTotal)
			if got.Total.Equal(decimal.RequireFromString(tt.baseTotal)) {
				t.Fatalf("group ratio was not applied: Total=%s equals base %s", got.Total, tt.baseTotal)
			}
			if !strings.Contains(got.CostSnapshot, "group_ratio=0.8") {
				t.Fatalf("CostSnapshot=%q want applied group_ratio=0.8", got.CostSnapshot)
			}
		})
	}
}

func TestResolve_DefaultGroupRatioIsOneAndKeepsLegacySnapshot(t *testing.T) {
	raw := json.RawMessage(`{"input_micro_usd":1000}`)
	flat := FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}

	got, err := Resolve(context.Background(), raw, Usage{InputTokens: 10}, flat, "price-v7")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	assertPricingDecimal(t, "Total", got.Total, "0.01")
	if got.CostSnapshot != "flat" {
		t.Fatalf("CostSnapshot=%q want legacy flat when no group ratio is configured", got.CostSnapshot)
	}
}

func assertPricingDecimal(t *testing.T, field string, got decimal.Decimal, want string) {
	t.Helper()
	wantDecimal := decimal.RequireFromString(want)
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s=%s want %s", field, got.String(), wantDecimal.String())
	}
}
