package settlementintent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const (
	preDeliveryOperationTimeout = 100 * time.Millisecond
	terminalOperationTimeout    = 2 * time.Second
	deliveryWaitHardLimit       = 500 * time.Millisecond
	completedResultGrace        = 5 * time.Millisecond
)

var (
	errIntentIDUnavailable = errors.New("结算意图标识不可用")
	errStoreCallTimeout    = &storeCallTimeoutError{}
	errStorePanicked       = &storePanicError{}
	errDeliveryWaitTimeout = &deliveryWaitTimeoutError{}
)

type storeCallTimeoutError struct{}

func (*storeCallTimeoutError) Error() string { return "结算意图存储调用超过硬上限" }

type storePanicError struct{}

func (*storePanicError) Error() string { return "结算意图存储发生异常" }

type deliveryWaitTimeoutError struct{}

func (*deliveryWaitTimeoutError) Error() string {
	return "结算意图交付状态等待超过硬上限"
}

var (
	noopAfterDelivery = func(time.Time) {}
	noopWaitDelivery  = func() {}
)

// Tracker 保存单个 HTTP 请求当前 attempt 的意图标识和乐观锁版本。
// 所有方法都只记录 warning，不把旁路存储错误返回给 relay 主链路。
type Tracker struct {
	store           Store
	enabled         bool
	mu              sync.Mutex
	insertAttempted bool
	id              int64
	version         int32
	tenantID        int64
	requestID       string
	claimID         int64
}

func NewTracker(store Store, enabled ...bool) *Tracker {
	active := store != nil
	if len(enabled) > 0 {
		active = enabled[0] || store != nil
	}
	return &Tracker{store: store, enabled: active}
}

// InsertPending 在 Reserve 成功后创建 pending 行，attemptSeq 只接受账本返回的权威值。
func (t *Tracker) InsertPending(ctx context.Context, tenantID int64, requestID, logicalRequestID string, claimID int64, attemptSeq int32, apiKeyID int64, requestFingerprint string, predictedCost decimal.Decimal) {
	if t == nil || !t.enabled {
		return
	}
	t.mu.Lock()
	t.insertAttempted = true
	t.id = 0
	t.version = 0
	t.tenantID = tenantID
	t.requestID = requestID
	t.claimID = claimID
	t.mu.Unlock()
	if t.store == nil {
		t.warn(ctx, "insert_pending", errStoreNotConfigured)
		return
	}
	in := CreateParams{
		TenantID: tenantID, RequestID: requestID, LogicalRequestID: logicalRequestID,
		ClaimID: claimID, AttemptSeq: attemptSeq, APIKeyID: apiKeyID, RequestFingerprint: requestFingerprint,
		PredictedCost: predictedCost,
	}
	opCtx, cancel := preDeliveryWriteContext(ctx)
	defer cancel()
	if err := opCtx.Err(); err != nil {
		t.warn(ctx, "insert_pending", err)
		return
	}
	id, err := callStoreWithinLimit(opCtx, preDeliveryOperationTimeout, func() (int64, error) {
		return t.store.Insert(opCtx, in)
	})
	if err != nil {
		t.warn(ctx, "insert_pending", err)
		return
	}
	if id == 0 {
		t.warn(ctx, "insert_pending", errIntentIDUnavailable)
		return
	}
	t.mu.Lock()
	t.id = id
	t.mu.Unlock()
}

func (t *Tracker) MarkDelivering(ctx context.Context, firstByteAt time.Time) {
	t.transition(ctx, "mark_delivering", preDeliveryWriteContext, preDeliveryOperationTimeout, func(opCtx context.Context, id int64, version int32) (int32, error) {
		return t.store.MarkDelivering(opCtx, id, version, firstByteAt)
	})
}

func (t *Tracker) MarkSettling(ctx context.Context, actualCost decimal.Decimal) {
	t.transition(ctx, "mark_settling", terminalWriteContext, terminalOperationTimeout, func(opCtx context.Context, id int64, version int32) (int32, error) {
		return t.store.MarkSettling(opCtx, id, version, actualCost)
	})
}

func (t *Tracker) MarkSettled(ctx context.Context, actualCost decimal.Decimal) {
	settledAt := time.Now().UTC()
	t.transition(ctx, "mark_settled", terminalWriteContext, terminalOperationTimeout, func(opCtx context.Context, id int64, version int32) (int32, error) {
		return t.store.MarkSettled(opCtx, id, version, actualCost, settledAt)
	})
}

func (t *Tracker) MarkAborted(ctx context.Context) {
	t.transition(ctx, "mark_aborted", terminalWriteContext, terminalOperationTimeout, func(opCtx context.Context, id int64, version int32) (int32, error) {
		return t.store.MarkAborted(opCtx, id, version)
	})
}

func (t *Tracker) MarkAbortResult(ctx context.Context, abortErr error) {
	if abortErr == nil {
		t.MarkAborted(ctx)
	}
}

func (t *Tracker) MarkFailed(ctx context.Context, actualCost decimal.Decimal) {
	t.transition(ctx, "mark_failed", terminalWriteContext, terminalOperationTimeout, func(opCtx context.Context, id int64, version int32) (int32, error) {
		return t.store.MarkFailed(opCtx, id, version, actualCost)
	})
}

// MarkSettlementResult 根据主结算的真实结果选择 settled、settling 或 failed，
// 只更新旁路意图，不改变主结算错误。
func (t *Tracker) MarkSettlementResult(ctx context.Context, actualCost decimal.Decimal, settleErr error, recoveryEnqueued bool) {
	if settleErr == nil {
		t.MarkSettled(ctx, actualCost)
		return
	}
	if recoveryEnqueued {
		t.MarkSettling(ctx, actualCost)
		return
	}
	t.MarkFailed(ctx, actualCost)
}

// WaitAndMarkSettlementResult 在主结算完成后有界等待交付态，再记录旁路终态。
func (t *Tracker) WaitAndMarkSettlementResult(ctx context.Context, actualCost decimal.Decimal, settleErr error, recoveryEnqueued bool, wait func()) {
	if wait != nil {
		wait()
	}
	t.MarkSettlementResult(ctx, actualCost, settleErr, recoveryEnqueued)
}

// AfterDeliveryAsync 返回首帧后的非阻塞记录函数，以及主链路结束后用于保持终态顺序的等待函数。
func (t *Tracker) AfterDeliveryAsync(ctx context.Context) (func(time.Time), func()) {
	if t == nil || !t.enabled || t.store == nil {
		return noopAfterDelivery, noopWaitDelivery
	}
	var once sync.Once
	started := make(chan struct{})
	done := make(chan struct{})
	after := func(firstByteAt time.Time) {
		once.Do(func() {
			close(started)
			go func() {
				defer close(done)
				defer func() {
					if recover() != nil {
						t.warn(ctx, "mark_delivering", errStorePanicked)
					}
				}()
				t.markDeliveringAsync(ctx, firstByteAt)
			}()
		})
	}
	wait := func() {
		select {
		case <-started:
			timer := time.NewTimer(deliveryWaitHardLimit)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				t.warn(ctx, "mark_delivering", errDeliveryWaitTimeout)
			}
		default:
		}
	}
	return after, wait
}

func (t *Tracker) markDeliveringAsync(ctx context.Context, firstByteAt time.Time) {
	store, id, version, ok := t.transitionSnapshot(ctx, "mark_delivering")
	if !ok {
		return
	}
	opCtx, cancel := preDeliveryWriteContext(ctx)
	defer cancel()
	if err := opCtx.Err(); err != nil {
		t.warn(ctx, "mark_delivering", err)
		return
	}
	result := callStoreSafely(func() (int32, error) {
		return store.MarkDelivering(opCtx, id, version, firstByteAt)
	})
	if result.err != nil {
		t.warn(ctx, "mark_delivering", result.err)
		return
	}
	t.applyVersion(id, version, result.value)
}

func (t *Tracker) transition(ctx context.Context, operation string, contextFactory func(context.Context) (context.Context, context.CancelFunc), hardLimit time.Duration, update func(context.Context, int64, int32) (int32, error)) {
	_, id, version, ok := t.transitionSnapshot(ctx, operation)
	if !ok {
		return
	}
	opCtx, cancel := contextFactory(ctx)
	defer cancel()
	if err := opCtx.Err(); err != nil {
		t.warn(ctx, operation, err)
		return
	}
	nextVersion, err := callStoreWithinLimit(opCtx, hardLimit, func() (int32, error) {
		return update(opCtx, id, version)
	})
	if err != nil {
		t.warn(ctx, operation, err)
		return
	}
	t.applyVersion(id, version, nextVersion)
}

func (t *Tracker) transitionSnapshot(ctx context.Context, operation string) (Store, int64, int32, bool) {
	if t == nil || !t.enabled {
		return nil, 0, 0, false
	}
	t.mu.Lock()
	insertAttempted := t.insertAttempted
	id := t.id
	version := t.version
	t.mu.Unlock()
	if !insertAttempted {
		return nil, 0, 0, false
	}
	store := t.store
	if store == nil {
		t.warn(ctx, operation, errStoreNotConfigured)
		return nil, 0, 0, false
	}
	if id == 0 {
		t.warn(ctx, operation, errIntentIDUnavailable)
		return nil, 0, 0, false
	}
	return store, id, version, true
}

func (t *Tracker) applyVersion(id int64, previous, next int32) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.id == id && t.version == previous {
		t.version = next
	}
}

func (t *Tracker) warn(ctx context.Context, operation string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	tenantID := t.tenantID
	requestID := t.requestID
	claimID := t.claimID
	t.mu.Unlock()
	slog.WarnContext(ctx, "结算意图旁路写失败，主链路继续",
		slog.String("operation", operation),
		slog.Int64("tenant_id", tenantID),
		slog.String("request_id", requestID),
		slog.Int64("claim_id", claimID),
		slog.String("error_type", fmt.Sprintf("%T", err)),
	)
}

type storeCallResult[T any] struct {
	value T
	err   error
}

func callStoreSafely[T any](call func() (T, error)) (result storeCallResult[T]) {
	defer func() {
		if recover() != nil {
			var zero T
			result = storeCallResult[T]{value: zero, err: errStorePanicked}
		}
	}()
	result.value, result.err = call()
	return result
}

func callStoreWithinLimit[T any](ctx context.Context, hardLimit time.Duration, call func() (T, error)) (T, error) {
	results := make(chan storeCallResult[T], 1)
	go func() {
		results <- callStoreSafely(call)
	}()
	timer := time.NewTimer(hardLimit)
	defer timer.Stop()
	select {
	case result := <-results:
		return result.value, result.err
	case <-ctx.Done():
		grace := time.NewTimer(completedResultGrace)
		defer grace.Stop()
		select {
		case result := <-results:
			return result.value, result.err
		case <-grace.C:
		}
		var zero T
		return zero, ctx.Err()
	case <-timer.C:
		var zero T
		return zero, errStoreCallTimeout
	}
}

func preDeliveryWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, preDeliveryOperationTimeout)
}

func terminalWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), terminalOperationTimeout)
}
