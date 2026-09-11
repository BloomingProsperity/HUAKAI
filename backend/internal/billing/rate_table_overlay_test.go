package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type overlayStub struct {
	data json.RawMessage
	err  error
	seen []json.RawMessage
}

func (s *overlayStub) OverlayPricingData(_ context.Context, data json.RawMessage) (json.RawMessage, error) {
	s.seen = append(s.seen, append(json.RawMessage(nil), data...))
	if s.err != nil {
		return nil, s.err
	}
	if len(s.data) > 0 {
		return s.data, nil
	}
	return data, nil
}

func TestGetRateTableAppliesOverlayButSnapshotDoesNot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 5, 0, 0, 0, time.UTC)
	official := json.RawMessage(`{"models":{"gpt-x":{"input_micro_usd":"4"}}}`)
	effective := json.RawMessage(`{"models":{"gpt-x":{"input_micro_usd":"1"}}}`)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{{
			id: 11, tenantID: 0, version: "1.0", isPublic: true,
			pricingData: official, effectiveFrom: now, createdAt: now,
		}},
	}
	overlay := &overlayStub{data: effective}
	source := &PGXRateTableSource{pool: stub, overlay: overlay}

	table, err := source.GetRateTable(ctx, "1.0")
	if err != nil {
		t.Fatalf("GetRateTable: %v", err)
	}
	if string(table.PricingData) != string(effective) {
		t.Fatalf("GetRateTable data=%s want overlayed", table.PricingData)
	}

	officialTable, err := source.GetOfficialRateTable(ctx, "1.0")
	if err != nil {
		t.Fatalf("GetOfficialRateTable: %v", err)
	}
	if string(officialTable.PricingData) != string(official) {
		t.Fatalf("GetOfficialRateTable=%s want official", officialTable.PricingData)
	}

	snapshot, err := source.GetRateTableSnapshot(ctx, 11)
	if err != nil {
		t.Fatalf("GetRateTableSnapshot: %v", err)
	}
	if string(snapshot.PricingData) != string(official) {
		t.Fatalf("snapshot must stay historical official, got %s", snapshot.PricingData)
	}
	if len(overlay.seen) != 1 {
		t.Fatalf("overlay calls=%d want only GetRateTable", len(overlay.seen))
	}
}

func TestGetRateTableFailsClosedWhenOverlayErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 5, 0, 0, 0, time.UTC)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{{
			id: 11, tenantID: 0, version: "1.0", isPublic: true,
			pricingData: json.RawMessage(`{"models":{}}`), effectiveFrom: now, createdAt: now,
		}},
	}
	source := &PGXRateTableSource{pool: stub, overlay: &overlayStub{err: errors.New("overlay down")}}
	if _, err := source.GetRateTable(ctx, "1.0"); err == nil {
		t.Fatal("GetRateTable must fail closed when overlay cannot load")
	}
}

func TestPublicModelPricesUsesOverlayedTable(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 5, 0, 0, 0, time.UTC)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{{
			id: 3, tenantID: 0, version: "1.0", isPublic: true,
			pricingData:   json.RawMessage(`{"models":{"gpt-4.1-mini":{"input_micro_usd":"0.40","output_micro_usd":"1.60"}}}`),
			effectiveFrom: now, createdAt: now,
		}},
	}
	source := &PGXRateTableSource{
		pool:    stub,
		overlay: &overlayStub{data: json.RawMessage(`{"models":{"gpt-4.1-mini":{"input_micro_usd":"0.10","output_micro_usd":"1.60"}}}`)},
	}
	table, err := source.PublicModelPrices(ctx, 0)
	if err != nil {
		t.Fatalf("PublicModelPrices: %v", err)
	}
	price, ok := table.Lookup("gpt-4.1-mini")
	if !ok {
		t.Fatal("missing overlaid public price")
	}
	if price.InputPerToken.String() != "0.0000001" {
		t.Fatalf("input=%s want overlayed 0.0000001", price.InputPerToken)
	}
	if price.OutputPerToken.String() != "0.0000016" {
		t.Fatalf("output=%s want official 0.0000016", price.OutputPerToken)
	}
}
