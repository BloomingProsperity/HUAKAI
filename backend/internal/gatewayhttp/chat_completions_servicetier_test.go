package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

func TestSettleCompletion_ServiceTierBillsActualFastAndCapsDowngrade(t *testing.T) {
	enableHCSFDispatchForTest(t)
	table := billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}
	base := decimal.RequireFromString("0.008")

	t.Run("requested_fast_actual_priority_double", func(t *testing.T) {
		autoGate := &recordingClaimGate{claimID: 8100}
		autoDeps := clientAdapterDeps(t)
		autoDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "default"}
		autoDeps.ClaimGate = autoGate
		autoDeps.Settler = &recordingSettler{}
		autoDeps.RateTables = &rateTableSourceStub{table: table}
		autoRec := invokeHandlerPath(t, autoDeps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if autoRec.Code != http.StatusOK {
			t.Fatalf("auto status=%d body=%s", autoRec.Code, autoRec.Body.String())
		}

		gate := &recordingClaimGate{claimID: 8101}
		settler := &recordingSettler{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "priority"}
		d.ClaimGate = gate
		d.Settler = settler
		d.RateTables = &rateTableSourceStub{table: table}
		rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","service_tier":"fast","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		want := base.Mul(decimal.NewFromInt(2))
		assertDecimalEqual(t, "ActualCost", settler.calls[0].ActualCost, want)
		reserveRatio := gate.req.PredictedCost.Div(autoGate.req.PredictedCost)
		if reserveRatio.LessThan(decimal.RequireFromString("1.9")) || reserveRatio.GreaterThan(decimal.RequireFromString("2.1")) {
			t.Fatalf("reserve ratio=%s (fast=%s auto=%s) want ~2", reserveRatio, gate.req.PredictedCost, autoGate.req.PredictedCost)
		}
		snap := settler.calls[0].Draft.CostSnapshot
		for _, mark := range []string{"service_tier_requested=fast", "service_tier_actual=priority", "service_tier_billed=priority", "service_tier_mult=2"} {
			if !strings.Contains(snap, mark) {
				t.Fatalf("snapshot=%q missing %s", snap, mark)
			}
		}
		if settler.calls[0].Draft.PendingReconciliation {
			t.Fatal("published fast lane must settle")
		}
		assertClientLane(t, rec.Body.Bytes(), "priority")
	})

	t.Run("requested_priority_actual_default_one", func(t *testing.T) {
		settler := &recordingSettler{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "default"}
		d.Settler = settler
		d.RateTables = &rateTableSourceStub{table: table}
		rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","service_tier":"priority","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertDecimalEqual(t, "downgrade ActualCost", settler.calls[0].ActualCost, base)
		if !strings.Contains(settler.calls[0].Draft.CostSnapshot, "service_tier_billed=default") {
			t.Fatalf("snapshot=%q want billed default", settler.calls[0].Draft.CostSnapshot)
		}
		assertClientLane(t, rec.Body.Bytes(), "default")
	})

	t.Run("requested_flex_actual_priority_stays_half", func(t *testing.T) {
		settler := &recordingSettler{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "priority"}
		d.Settler = settler
		d.RateTables = &rateTableSourceStub{table: table}
		rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","service_tier":"flex","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertDecimalEqual(t, "flex cap", settler.calls[0].ActualCost, base.Mul(decimal.RequireFromString("0.5")))
		if settler.calls[0].ActualCost.Equal(base.Mul(decimal.NewFromInt(2))) {
			t.Fatal("flex request was upgraded to fast; only-down broken")
		}
		assertClientLane(t, rec.Body.Bytes(), "priority")
	})

	t.Run("auto_follows_actual_priority", func(t *testing.T) {
		settler := &recordingSettler{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "priority"}
		d.Settler = settler
		d.RateTables = &rateTableSourceStub{table: table}
		rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertDecimalEqual(t, "auto actual", settler.calls[0].ActualCost, base.Mul(decimal.NewFromInt(2)))
	})

	t.Run("ultrafast_pending_not_invented", func(t *testing.T) {
		settler := &recordingSettler{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{actualLane: "ultrafast"}
		d.Settler = settler
		d.RateTables = &rateTableSourceStub{table: table}
		rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","service_tier":"ultrafast","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertDecimalEqual(t, "ultrafast provisional", settler.calls[0].ActualCost, base)
		if !settler.calls[0].Draft.PendingReconciliation {
			t.Fatal("unpublished lane must pending")
		}
		if !strings.Contains(settler.calls[0].Draft.CostSnapshot, "pending_reconciliation=service_tier") {
			t.Fatalf("snapshot=%q want unpublished pending marker", settler.calls[0].Draft.CostSnapshot)
		}
		assertClientLane(t, rec.Body.Bytes(), "ultrafast")
	})
}

func assertClientLane(t *testing.T, body []byte, want string) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("client body json: %v raw=%s", err, body)
	}
	got, _ := obj["service_tier"].(string)
	if got != want {
		t.Fatalf("client service_tier=%q want %q body=%s", got, want, body)
	}
}
