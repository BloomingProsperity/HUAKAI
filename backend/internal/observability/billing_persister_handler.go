package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

var ErrSettlerNotConfigured = errors.New("observability: billing settler not configured")

type BillingPersisterHandler struct {
	settler    billing.Settler
	timeout    time.Duration
	reconciler *DualRunReconciler
}

type BillingPersisterOption func(*BillingPersisterHandler)

func WithBillingPersisterReconciler(r *DualRunReconciler) BillingPersisterOption {
	return func(h *BillingPersisterHandler) { h.reconciler = r }
}

func NewBillingPersisterHandler(settler billing.Settler, timeout time.Duration, opts ...BillingPersisterOption) *BillingPersisterHandler {
	h := &BillingPersisterHandler{settler: settler, timeout: timeout}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *BillingPersisterHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerBillingPersister
}

func (h *BillingPersisterHandler) Tier() eventbus.Tier {
	return eventbus.TierHigh
}

func (h *BillingPersisterHandler) Order() int {
	return 10
}

func (h *BillingPersisterHandler) Critical() bool {
	return true
}

func (h *BillingPersisterHandler) Timeout() time.Duration {
	return h.timeout
}

func (h *BillingPersisterHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindUsageRecord
}

func (h *BillingPersisterHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	if h == nil || h.settler == nil {
		return ErrSettlerNotConfigured
	}
	req := event.SettleRequest
	if req.TenantID == 0 {
		req.TenantID = event.TenantID
	}
	if req.ClaimID == 0 {
		req.ClaimID = event.ClaimID
	}
	if req.AccountID == 0 {
		req.AccountID = event.AccountID
	}
	res, err := h.settler.Settle(ctx, req)
	if h.reconciler != nil {
		h.reconciler.RecordAsync(event, req, res, err)
	}
	if err != nil {
		return fmt.Errorf("observability: billing persister settle: %w", err)
	}
	return nil
}
