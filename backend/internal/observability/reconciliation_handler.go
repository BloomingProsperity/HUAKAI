package observability

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

const DefaultDualRunWindow = 7 * 24 * time.Hour

type SettlementObservation struct {
	EventID    string
	ClaimID    int64
	Path       string
	ActualCost decimal.Decimal
	Error      string
	ObservedAt time.Time
}

type ReconciliationResult struct {
	EventID       string
	ClaimID       int64
	Matched       bool
	LegacyMissing bool
	AsyncMissing  bool
	CostMismatch  bool
	ErrorMismatch bool
	Legacy        SettlementObservation
	Async         SettlementObservation
}

type reconciliationPair struct {
	legacy *SettlementObservation
	async  *SettlementObservation
}

type DualRunReconciler struct {
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	items map[string]*reconciliationPair
}

func NewDualRunReconciler(window time.Duration) *DualRunReconciler {
	if window <= 0 {
		window = DefaultDualRunWindow
	}
	return &DualRunReconciler{
		window: window,
		now:    func() time.Time { return time.Now().UTC() },
		items:  make(map[string]*reconciliationPair),
	}
}

func (r *DualRunReconciler) RecordLegacy(event eventbus.RequestCompletionEvent, req billing.SettleRequest, _ *billing.SettleResult, err error) {
	r.record(event, req, "legacy", err)
}

func (r *DualRunReconciler) RecordAsync(event eventbus.RequestCompletionEvent, req billing.SettleRequest, _ *billing.SettleResult, err error) {
	r.record(event, req, "async", err)
}

func (r *DualRunReconciler) Compare(eventID string) (ReconciliationResult, bool) {
	if r == nil {
		return ReconciliationResult{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pair := r.items[eventID]
	if pair == nil {
		return ReconciliationResult{}, false
	}
	return comparePair(eventID, pair), true
}

func (r *DualRunReconciler) ExpiredMismatches(now time.Time) []ReconciliationResult {
	if r == nil {
		return nil
	}
	if now.IsZero() {
		now = r.now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ReconciliationResult
	for eventID, pair := range r.items {
		res := comparePair(eventID, pair)
		oldest := res.Legacy.ObservedAt
		if oldest.IsZero() || (!res.Async.ObservedAt.IsZero() && res.Async.ObservedAt.Before(oldest)) {
			oldest = res.Async.ObservedAt
		}
		if oldest.IsZero() || now.Sub(oldest) < r.window || res.Matched {
			continue
		}
		out = append(out, res)
	}
	return out
}

func (r *DualRunReconciler) record(event eventbus.RequestCompletionEvent, req billing.SettleRequest, path string, err error) {
	if r == nil {
		return
	}
	eventID := event.ID
	if eventID == "" {
		eventID = event.RequestID
	}
	if eventID == "" {
		eventID = "claim"
	}
	obs := &SettlementObservation{
		EventID:    eventID,
		ClaimID:    req.ClaimID,
		Path:       path,
		ActualCost: req.ActualCost,
		ObservedAt: r.now(),
	}
	if obs.ClaimID == 0 {
		obs.ClaimID = event.ClaimID
	}
	if err != nil {
		obs.Error = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pair := r.items[eventID]
	if pair == nil {
		pair = &reconciliationPair{}
		r.items[eventID] = pair
	}
	if path == "legacy" {
		pair.legacy = obs
	} else {
		pair.async = obs
	}
}

func comparePair(eventID string, pair *reconciliationPair) ReconciliationResult {
	res := ReconciliationResult{EventID: eventID}
	if pair == nil {
		res.LegacyMissing = true
		res.AsyncMissing = true
		return res
	}
	if pair.legacy == nil {
		res.LegacyMissing = true
	} else {
		res.Legacy = *pair.legacy
		res.ClaimID = pair.legacy.ClaimID
	}
	if pair.async == nil {
		res.AsyncMissing = true
	} else {
		res.Async = *pair.async
		if res.ClaimID == 0 {
			res.ClaimID = pair.async.ClaimID
		}
	}
	if pair.legacy != nil && pair.async != nil {
		res.CostMismatch = !pair.legacy.ActualCost.Equal(pair.async.ActualCost)
		res.ErrorMismatch = pair.legacy.Error != pair.async.Error
		res.Matched = !res.CostMismatch && !res.ErrorMismatch
	}
	return res
}

type ReconciliationHandler struct {
	timeout    time.Duration
	reconciler *DualRunReconciler
}

func NewReconciliationHandler(timeout time.Duration, reconciler *DualRunReconciler) *ReconciliationHandler {
	return &ReconciliationHandler{timeout: timeout, reconciler: reconciler}
}

func (h *ReconciliationHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerReconciliationCheck
}

func (h *ReconciliationHandler) Tier() eventbus.Tier {
	return eventbus.TierMed
}

func (h *ReconciliationHandler) Order() int {
	return 30
}

func (h *ReconciliationHandler) Critical() bool {
	return false
}

func (h *ReconciliationHandler) Timeout() time.Duration {
	return h.timeout
}

func (h *ReconciliationHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindMetrics
}

func (h *ReconciliationHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
