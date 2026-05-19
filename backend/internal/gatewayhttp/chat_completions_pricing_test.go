package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

func TestSettleCompletion_RateTableMiss_FailsClosed(t *testing.T) {
	enableHCSFDispatchForTest(t)
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.RateTables = &rateTableSourceStub{err: billing.ErrRateTableNotFound}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pricing_unavailable") {
		t.Fatalf("body=%s want pricing_unavailable", rec.Body.String())
	}
}

func TestSettleCompletion_UsesRateTableActualCost(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	want := decimal.RequireFromString("0.008")
	if !settler.calls[0].ActualCost.Equal(want) {
		t.Fatalf("ActualCost=%s want %s", settler.calls[0].ActualCost, want)
	}
	if !settler.calls[0].Draft.ActualCost.Equal(want) {
		t.Fatalf("Draft.ActualCost=%s want %s", settler.calls[0].Draft.ActualCost, want)
	}
}
