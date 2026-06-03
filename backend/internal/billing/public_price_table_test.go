package billing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicPriceTableParsesCustomerUSDPerTokenAndFoldsMultiplier(t *testing.T) {
	raw := json.RawMessage(`{
		"models": {
			"anthropic/claude-opus-4": {
				"input_micro_usd": 0.40,
				"output_micro_usd": "1.60",
				"model_multiplier": 2.0,
				"cache_read_micro_usd": 0.10
			}
		}
	}`)

	table, err := parsePublicPriceTable("public-v1", raw)
	if err != nil {
		t.Fatalf("parsePublicPriceTable: %v", err)
	}
	price, ok := table.Lookup("Claude Opus Public", "anthropic/claude-opus-4")
	if !ok {
		t.Fatal("Lookup did not match canonical id fallback")
	}
	if !price.HasInput || price.InputPerToken.String() != "0.0000008" {
		t.Fatalf("input price=%s has=%v want 0.0000008 after multiplier", price.InputPerToken, price.HasInput)
	}
	if !price.HasOutput || price.OutputPerToken.String() != "0.0000032" {
		t.Fatalf("output price=%s has=%v want 0.0000032 after multiplier", price.OutputPerToken, price.HasOutput)
	}
	if _, ok := table.Lookup("Claude Opus Public"); ok {
		t.Fatal("display alias alone unexpectedly matched canonical-only price")
	}
	if table.Version != "public-v1" {
		t.Fatalf("version=%q want public-v1", table.Version)
	}
}

func TestPublicModelPricesUsesCurrentPublicTenantRowOrPlatformDefault(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{
			{
				id:            701,
				tenantID:      7,
				version:       "tenant-private",
				isPublic:      false,
				pricingData:   json.RawMessage(`{"models":{"gpt-4.1-mini":{"input_micro_usd":9.90,"output_micro_usd":9.90}}}`),
				effectiveFrom: now.Add(-time.Minute),
				createdAt:     now.Add(-time.Minute),
			},
			{
				id:            101,
				tenantID:      0,
				version:       "platform-public",
				isPublic:      true,
				pricingData:   json.RawMessage(`{"models":{"gpt-4.1-mini":{"input_micro_usd":0.40,"output_micro_usd":1.60}}}`),
				effectiveFrom: now.Add(-time.Hour),
				createdAt:     now.Add(-time.Hour),
			},
		},
	}
	source := &PGXRateTableSource{pool: stub}

	table, err := source.PublicModelPrices(ctx, 7)
	if err != nil {
		t.Fatalf("PublicModelPrices: %v", err)
	}
	price, ok := table.Lookup("gpt-4.1-mini")
	if !ok {
		t.Fatalf("Lookup missing platform public fallback price; version=%q calls=%#v", table.Version, stub.calls)
	}
	if price.InputPerToken.String() != "0.0000004" || price.OutputPerToken.String() != "0.0000016" {
		t.Fatalf("price input=%s output=%s want platform public customer prices", price.InputPerToken, price.OutputPerToken)
	}
	if table.Version != "platform-public" {
		t.Fatalf("version=%q want platform-public fallback", table.Version)
	}
	if len(stub.calls) != 2 {
		t.Fatalf("query calls=%d want tenant current lookup then platform fallback", len(stub.calls))
	}
	for _, call := range stub.calls {
		sql := strings.ToLower(call.sql)
		if !strings.Contains(sql, "is_public = true") {
			t.Fatalf("PublicModelPrices SQL must scope to customer-public rows:\n%s", call.sql)
		}
		if !strings.Contains(sql, "tenant_id = $1") {
			t.Fatalf("PublicModelPrices SQL must tenant-scope current lookup:\n%s", call.sql)
		}
		if !strings.Contains(sql, "effective_to is null") {
			t.Fatalf("PublicModelPrices SQL must select only current rows:\n%s", call.sql)
		}
	}
}
