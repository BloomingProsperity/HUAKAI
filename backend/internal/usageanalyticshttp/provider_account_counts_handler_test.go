package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// MUTATION: call the query without from/to window or collapse provider accounts -> JSON contract fails -> RED.
func TestProviderAccountCountsHandler(t *testing.T) {
	store := &providerAccountCountsStoreStub{rows: []dbbilling.AggregateUsageCountsByProviderAccountRow{
		{ProviderAccountID: 1001, RequestCount: 2, TotalInputTokens: 40, TotalOutputTokens: 60, TotalCost: "3.00000000"},
		{ProviderAccountID: 1002, RequestCount: 1, TotalInputTokens: 5, TotalOutputTokens: 6, TotalCost: "0.50000000"},
	}}

	rec := invoke(NewProviderAccountCountsHandler(store), "/v1/admin/usage/provider-account-counts?from=2026-06-08T10:00:00Z&to=2026-06-08T11:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Counts []struct {
			ProviderAccountID int64  `json:"provider_account_id"`
			RequestCount      int64  `json:"request_count"`
			TotalInputTokens  int64  `json:"total_input_tokens"`
			TotalOutputTokens int64  `json:"total_output_tokens"`
			TotalCost         string `json:"total_cost"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if len(body.Counts) != 2 {
		t.Fatalf("counts=%v want 2 provider account rows", body.Counts)
	}
	if body.Counts[0].ProviderAccountID != 1001 || body.Counts[0].RequestCount != 2 || body.Counts[0].TotalCost != "3.00000000" {
		t.Fatalf("first count=%+v want provider account 1001 count=2 cost=3", body.Counts[0])
	}
	if body.Counts[1].ProviderAccountID != 1002 || body.Counts[1].RequestCount != 1 || body.Counts[1].TotalCost != "0.50000000" {
		t.Fatalf("second count=%+v want provider account 1002 count=1 cost=0.5", body.Counts[1])
	}
	if !store.arg.FromTs.Valid || !store.arg.ToTs.Valid {
		t.Fatalf("from/to not passed as valid timestamptz: %+v", store.arg)
	}
	if !store.arg.FromTs.Time.Equal(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("from arg=%s want 2026-06-08T10:00:00Z", store.arg.FromTs.Time)
	}
	if !store.arg.ToTs.Time.Equal(time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("to arg=%s want 2026-06-08T11:00:00Z", store.arg.ToTs.Time)
	}
}

func TestProviderAccountCountsHandlerInvalidWindowDoesNotQuery(t *testing.T) {
	for _, target := range []string{
		"/v1/admin/usage/provider-account-counts",
		"/v1/admin/usage/provider-account-counts?from=bad&to=2026-06-08T11:00:00Z",
		"/v1/admin/usage/provider-account-counts?from=2026-06-08T11:00:00Z&to=2026-06-08T10:00:00Z",
	} {
		store := &providerAccountCountsStoreStub{}
		rec := invoke(NewProviderAccountCountsHandler(store), target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s code=%d want 400 body=%s", target, rec.Code, rec.Body.String())
		}
		if store.calls != 0 {
			t.Fatalf("%s query calls=%d want 0 for invalid window", target, store.calls)
		}
	}
}

type providerAccountCountsStoreStub struct {
	rows  []dbbilling.AggregateUsageCountsByProviderAccountRow
	arg   dbbilling.AggregateUsageCountsByProviderAccountParams
	calls int
}

func (s *providerAccountCountsStoreStub) AggregateUsageCountsByProviderAccount(_ context.Context, arg dbbilling.AggregateUsageCountsByProviderAccountParams) ([]dbbilling.AggregateUsageCountsByProviderAccountRow, error) {
	s.calls++
	s.arg = arg
	return s.rows, nil
}
