package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

type fakeSettler struct {
	req billing.SettleRequest
	err error
}

func (s *fakeSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.req = req
	if s.err != nil {
		return nil, s.err
	}
	return &billing.SettleResult{}, nil
}

func (s *fakeSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *fakeSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

func TestBillingPersisterRecordsAsyncReconciliation(t *testing.T) {
	reconciler := NewDualRunReconciler(DefaultDualRunWindow)
	settler := &fakeSettler{}
	handler := NewBillingPersisterHandler(settler, time.Second, WithBillingPersisterReconciler(reconciler))
	req := billing.SettleRequest{
		TenantID:   7,
		ClaimID:    101,
		ActualCost: decimal.RequireFromString("0.01000000"),
	}
	event := eventbus.RequestCompletionEvent{ID: "evt-reconcile", TenantID: 7, ClaimID: 101, SettleRequest: req}
	reconciler.RecordLegacy(event, req, &billing.SettleResult{}, nil)

	if err := handler.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if settler.req.ClaimID != 101 {
		t.Fatalf("settler claim=%d want 101", settler.req.ClaimID)
	}
	got, ok := reconciler.Compare("evt-reconcile")
	if !ok {
		t.Fatal("missing reconciliation result")
	}
	if !got.Matched || got.CostMismatch || got.ErrorMismatch {
		t.Fatalf("reconciliation=%+v want matched", got)
	}
}

func TestDualRunReconcilerExpiresMismatchesAfterSevenDays(t *testing.T) {
	reconciler := NewDualRunReconciler(DefaultDualRunWindow)
	base := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	reconciler.now = func() time.Time { return base }
	event := eventbus.RequestCompletionEvent{ID: "evt-mismatch", TenantID: 7, ClaimID: 202}
	legacyReq := billing.SettleRequest{TenantID: 7, ClaimID: 202, ActualCost: decimal.RequireFromString("0.01000000")}
	asyncReq := billing.SettleRequest{TenantID: 7, ClaimID: 202, ActualCost: decimal.RequireFromString("0.02000000")}
	reconciler.RecordLegacy(event, legacyReq, &billing.SettleResult{}, nil)
	reconciler.RecordAsync(event, asyncReq, &billing.SettleResult{}, errors.New("async mismatch"))

	got, ok := reconciler.Compare("evt-mismatch")
	if !ok || got.Matched || !got.CostMismatch || !got.ErrorMismatch {
		t.Fatalf("compare=%+v ok=%v want cost+error mismatch", got, ok)
	}
	if early := reconciler.ExpiredMismatches(base.Add(6 * 24 * time.Hour)); len(early) != 0 {
		t.Fatalf("early expired mismatches=%d want 0", len(early))
	}
	expired := reconciler.ExpiredMismatches(base.Add(8 * 24 * time.Hour))
	if len(expired) != 1 || expired[0].EventID != "evt-mismatch" {
		t.Fatalf("expired=%+v want evt-mismatch", expired)
	}
}
