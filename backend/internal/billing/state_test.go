package billing

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestStreamStateTransitionMatrix(t *testing.T) {
	cases := []struct {
		from StreamState
		to   StreamState
		ok   bool
	}{
		{StreamStateAcquired, StreamStateAcquired, true},
		{StreamStateAcquired, StreamStateInFlight, true},
		{StreamStateAcquired, StreamStatePartial, false},
		{StreamStateAcquired, StreamStateFailed, true},
		{StreamStateInFlight, StreamStateAcquired, false},
		{StreamStateInFlight, StreamStateInFlight, true},
		{StreamStateInFlight, StreamStatePartial, true},
		{StreamStateInFlight, StreamStateFailed, true},
		{StreamStatePartial, StreamStateInFlight, false},
		{StreamStatePartial, StreamStatePartial, true},
		{StreamStatePartial, StreamStateFailed, false},
		{StreamStateFailed, StreamStateInFlight, false},
		{StreamStateFailed, StreamStatePartial, false},
		{StreamStateFailed, StreamStateFailed, true},
	}
	for _, tc := range cases {
		got := CanTransitionStreamState(tc.from, tc.to)
		if got != tc.ok {
			t.Fatalf("%s -> %s got %v want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestAttemptFromGatewayDraftPartialClientGone(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.ClientDisconnect,
		DeliveredTokenCount: 3,
	})
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount != 3 {
		t.Fatalf("delivered=%d want 3", attempt.DeliveredTokenCount)
	}
	if attempt.StreamTerminatedReason != "client_gone" {
		t.Fatalf("reason=%q want client_gone", attempt.StreamTerminatedReason)
	}
}

func TestAttemptFromGatewayDraftFailedNoCharge(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass: gateway.TotalStreamTimeout,
	})
	if attempt.State != StreamStateFailed {
		t.Fatalf("state=%s want failed", attempt.State)
	}
	if got := CostForAttempt(decimal.RequireFromString("0.01000000"), attempt); !got.IsZero() {
		t.Fatalf("failed attempt cost=%s want zero", got)
	}
	if attempt.StreamTerminatedReason != "upstream_timeout" {
		t.Fatalf("reason=%q want upstream_timeout", attempt.StreamTerminatedReason)
	}
}

func TestAttemptFromGatewayDraftOutputUsageWinsOverChunkFallback(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.UpstreamError5xx,
		TokensOutput:        19,
		DeliveredTokenCount: 2,
	})
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount != 19 {
		t.Fatalf("delivered=%d want authoritative output tokens 19", attempt.DeliveredTokenCount)
	}
	if got := CostForAttempt(decimal.RequireFromString("0.01000000"), attempt); got.IsZero() {
		t.Fatal("partial attempt should remain chargeable")
	}
}

func TestAttemptReasonClamped(t *testing.T) {
	long := strings.Repeat("x", 80)
	attempt := (Attempt{State: StreamStateFailed, StreamTerminatedReason: long}).Normalized()
	if len(attempt.StreamTerminatedReason) != maxStreamTerminatedReasonLen {
		t.Fatalf("reason length=%d want %d", len(attempt.StreamTerminatedReason), maxStreamTerminatedReasonLen)
	}
}

func TestAT_AUDIT_001_060_ZeroRefundReturnsSkippedCode(t *testing.T) {
	res := zeroRefundResult()
	if res == nil || res.RefundMicroUSD != 0 || res.AdjustmentRef != RefundSkippedAmountZeroRef {
		t.Fatalf("zero refund result=%+v want adjustment_ref %q", res, RefundSkippedAmountZeroRef)
	}
	if res.AdjustmentRef == "billing_refund:zero" {
		t.Fatalf("zero refund must not use ambiguous legacy ref %q", res.AdjustmentRef)
	}
}

func TestAT_AUDIT_001_062_BillingRefundCostOverflowRejected(t *testing.T) {
	_, err := costUSDToMicros(decimal.RequireFromString("9223372036854.775808"))
	if !errors.Is(err, ErrCostOverflow) {
		t.Fatalf("overflow error=%v want %v", err, ErrCostOverflow)
	}
}
