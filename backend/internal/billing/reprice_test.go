package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

type repriceRatioStub struct {
	ratio   decimal.Decimal
	pending bool
	err     error
}

func (s repriceRatioStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s.err != nil {
		return decimal.Zero, s.err
	}
	return s.ratio, nil
}

func (s repriceRatioStub) ResolveWithSignal(context.Context, int64, int64) (decimal.Decimal, bool, error) {
	if s.err != nil {
		return decimal.Zero, false, s.err
	}
	return s.ratio, s.pending, nil
}

func TestRepriceCostUsesCurrentRateTableAndRatio(t *testing.T) {
	table := RateTable{
		ID:      55,
		Version: "current-v1",
		PricingData: []byte(`{
			"providers":{
				"openai":{
					"models":{
						"gpt-4o":{
							"input_micro_usd":"1000",
							"output_micro_usd":"2000"
						}
					}
				}
			}
		}`),
	}
	row := repriceUsageRecordRow{
		ID:             11,
		TenantID:       7,
		PoolGroupID:    3,
		ProviderCode:   "openai",
		ProtocolFamily: "openai_chat",
		TokensInput:    100,
		TokensOutput:   50,
		RequestedModel: "gpt-4o",
		ActualCost:     decimal.RequireFromString("0.05000000"),
	}
	got, source, err := repriceCostFromCurrentPricing(context.Background(), table, row, decimal.RequireFromString("0.5"))
	if err != nil {
		t.Fatalf("repriceCostFromCurrentPricing: %v", err)
	}
	if got.StringFixed(8) != "0.10000000" {
		t.Fatalf("cost=%s want 0.10000000", got.StringFixed(8))
	}
	for _, want := range []string{
		"billing_policy_version=current-v1",
		"rate_table_id=55",
		"rate_source=providers.openai.models.gpt-4o",
		"group_ratio=0.5",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("pricing source %q missing %q", source, want)
		}
	}
}

func TestRepriceRejectsRatioFallbackAsAuthoritative(t *testing.T) {
	svc := &RepriceService{PricingRatioResolver: repriceRatioStub{ratio: decimal.NewFromInt(1), pending: true}}
	_, err := svc.groupRatio(context.Background(), repriceUsageRecordRow{TenantID: 7, PoolGroupID: 3})
	if !errors.Is(err, ErrRepricePricingUnavailable) {
		t.Fatalf("err=%v want ErrRepricePricingUnavailable", err)
	}
}

func TestNormalizeRepriceRequestBatchLimit(t *testing.T) {
	req, err := normalizeRepriceRequest(RepriceRequest{UsageRecordID: 11})
	if err != nil {
		t.Fatalf("normalize single: %v", err)
	}
	if req.Limit != RepriceMaxBatchLimit || req.Source != RepriceDefaultSource {
		t.Fatalf("req=%+v want default limit/source", req)
	}
	if _, err := normalizeRepriceRequest(RepriceRequest{UsageRecordID: 11, Limit: RepriceMaxBatchLimit + 1}); !errors.Is(err, ErrRepriceInvalidInput) {
		t.Fatalf("err=%v want ErrRepriceInvalidInput", err)
	}
}
