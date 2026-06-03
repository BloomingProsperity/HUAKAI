package quotaenforce

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func TestSettlerSettleFinalizesQuotaAfterBillingSuccess(t *testing.T) {
	inner := &recordingBillingSettler{}
	finalizer := &recordingQuotaFinalizer{}
	settler := NewSettler(inner, finalizer)
	req := billing.SettleRequest{
		TenantID:       7,
		ClaimID:        9001,
		ActualCost:     decimal.RequireFromString("0.125"),
		AuditRequestID: "req-settle",
	}

	result, err := settler.Settle(context.Background(), req)

	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if result == nil {
		t.Fatal("Settle result is nil")
	}
	if got := inner.events; !reflect.DeepEqual(got, []string{"billing_settle"}) {
		t.Fatalf("inner events=%v want billing settle first", got)
	}
	if got := finalizer.events; !reflect.DeepEqual(got, []string{"quota_settle"}) {
		t.Fatalf("quota events=%v want quota settle after billing success", got)
	}
	if finalizer.settleReq.TenantID != req.TenantID || finalizer.settleReq.ClaimID != req.ClaimID {
		t.Fatalf("quota settle identity=%+v want tenant=%d claim=%d", finalizer.settleReq, req.TenantID, req.ClaimID)
	}
	if !finalizer.settleReq.ActualCost.Equal(req.ActualCost) {
		t.Fatalf("quota actual cost=%s want billing actual cost %s", finalizer.settleReq.ActualCost, req.ActualCost)
	}
}

func TestSettlerSettleDoesNotFinalizeQuotaWhenBillingFails(t *testing.T) {
	inner := &recordingBillingSettler{settleErr: errors.New("billing settle failed")}
	finalizer := &recordingQuotaFinalizer{}
	settler := NewSettler(inner, finalizer)

	_, err := settler.Settle(context.Background(), billing.SettleRequest{TenantID: 7, ClaimID: 9002})

	if err == nil || !strings.Contains(err.Error(), "billing settle failed") {
		t.Fatalf("Settle err=%v want billing failure", err)
	}
	if len(finalizer.events) != 0 {
		t.Fatalf("quota finalizer events=%v want none when billing settle fails", finalizer.events)
	}
}

func TestSettlerAbortReleasesQuotaAfterBillingAbortSuccess(t *testing.T) {
	inner := &recordingBillingSettler{}
	finalizer := &recordingQuotaFinalizer{}
	settler := NewSettler(inner, finalizer)
	protocolLoss := json.RawMessage(`{"loss":true}`)

	err := settler.Abort(context.Background(), 7, 9003, "queue_wait", "req-abort", 11, protocolLoss)

	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if inner.abortReason != "queue_wait" {
		t.Fatalf("inner abort reason=%q want original billing reason queue_wait", inner.abortReason)
	}
	if got := inner.events; !reflect.DeepEqual(got, []string{"billing_abort"}) {
		t.Fatalf("inner events=%v want billing abort first", got)
	}
	if got := finalizer.events; !reflect.DeepEqual(got, []string{"quota_release"}) {
		t.Fatalf("quota events=%v want quota release after billing abort", got)
	}
	if finalizer.releaseReq.TenantID != 7 || finalizer.releaseReq.ClaimID != 9003 {
		t.Fatalf("quota release identity=%+v want tenant=7 claim=9003", finalizer.releaseReq)
	}
	if finalizer.releaseReq.Reason != "abort" {
		t.Fatalf("quota release reason=%q want normalized abort for unsupported billing reason", finalizer.releaseReq.Reason)
	}
}

func TestSettlerAbortIgnoresMissingQuotaReservationAfterQuotaDeny(t *testing.T) {
	inner := &recordingBillingSettler{}
	finalizer := &recordingQuotaFinalizer{releaseErr: quota.ErrReservationNotFound}
	settler := NewSettler(inner, finalizer)

	err := settler.Abort(context.Background(), 7, 9004, "quota_denied", "req-deny", 0, nil)

	if err != nil {
		t.Fatalf("Abort with missing quota reservation: %v", err)
	}
	if inner.abortReason != "quota_denied" {
		t.Fatalf("inner abort reason=%q want quota_denied", inner.abortReason)
	}
}

func TestSettlerCommitCacheHitFinalizesQuotaAsCacheHit(t *testing.T) {
	inner := &recordingBillingSettler{}
	finalizer := &recordingQuotaFinalizer{}
	settler := NewSettler(inner, finalizer)
	req := billing.SettleRequest{TenantID: 7, ClaimID: 9005, AuditRequestID: "req-cache", Fingerprint: "fp-cache"}

	if err := settler.CommitCacheHit(context.Background(), req); err != nil {
		t.Fatalf("CommitCacheHit: %v", err)
	}
	if got := inner.events; !reflect.DeepEqual(got, []string{"billing_cache_hit"}) {
		t.Fatalf("inner events=%v want billing cache hit first", got)
	}
	if got := finalizer.events; !reflect.DeepEqual(got, []string{"quota_cache_hit"}) {
		t.Fatalf("quota events=%v want quota cache-hit finalization", got)
	}
	if finalizer.cacheReq.TenantID != req.TenantID || finalizer.cacheReq.ClaimID != req.ClaimID {
		t.Fatalf("quota cache identity=%+v want tenant=%d claim=%d", finalizer.cacheReq, req.TenantID, req.ClaimID)
	}
}

func TestNewSettlerNilQuotaIsPlainPassThrough(t *testing.T) {
	inner := &recordingBillingSettler{}

	settler := NewSettler(inner, nil)

	if settler != inner {
		t.Fatalf("NewSettler(inner, nil) returned %T want original plain settler", settler)
	}
}

type recordingBillingSettler struct {
	events      []string
	abortReason string
	settleErr   error
}

func (s *recordingBillingSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	s.events = append(s.events, "billing_settle")
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

func (s *recordingBillingSettler) Abort(_ context.Context, _ int64, _ int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.events = append(s.events, "billing_abort")
	s.abortReason = reason
	return nil
}

func (s *recordingBillingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.events = append(s.events, "billing_cache_hit")
	return nil
}

func (s *recordingBillingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	s.events = append(s.events, "billing_refund")
	return &billing.RefundResult{}, nil
}

type recordingQuotaFinalizer struct {
	events     []string
	settleReq  quota.SettleRequest
	releaseReq quota.ReleaseRequest
	cacheReq   quota.CacheHitRequest
	releaseErr error
}

func (s *recordingQuotaFinalizer) Settle(_ context.Context, req quota.SettleRequest) (quota.SettleResult, error) {
	s.events = append(s.events, "quota_settle")
	s.settleReq = req
	return quota.SettleResult{}, nil
}

func (s *recordingQuotaFinalizer) Release(_ context.Context, req quota.ReleaseRequest) (quota.ReleaseResult, error) {
	s.events = append(s.events, "quota_release")
	s.releaseReq = req
	return quota.ReleaseResult{}, s.releaseErr
}

func (s *recordingQuotaFinalizer) CommitCacheHit(_ context.Context, req quota.CacheHitRequest) (quota.CacheHitResult, error) {
	s.events = append(s.events, "quota_cache_hit")
	s.cacheReq = req
	return quota.CacheHitResult{}, nil
}
