package eventbus

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type Option func(*Bus)

type Bus struct {
	cfg    Config
	dlq    DLQSink
	onDrop func(DropNotice)

	mu       sync.RWMutex
	handlers []*handlerRunner
	states   map[StateKey]StateSnapshot
	closed   bool
	now      func() time.Time
}

func New(cfg Config, opts ...Option) *Bus {
	cfg = NormalizeConfig(cfg)
	b := &Bus{
		cfg:    cfg,
		states: make(map[StateKey]StateSnapshot),
		now:    func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func WithDLQ(sink DLQSink) Option {
	return func(b *Bus) { b.dlq = sink }
}

func WithDropHook(hook func(DropNotice)) Option {
	return func(b *Bus) { b.onDrop = hook }
}

func WithClock(now func() time.Time) Option {
	return func(b *Bus) {
		if now != nil {
			b.now = now
		}
	}
}

func (b *Bus) Register(h Handler) error {
	if b == nil {
		return ErrBusClosed
	}
	if h == nil || h.ID() == "" {
		return ErrInvalidHandler
	}
	r := newHandlerRunner(b, h)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBusClosed
	}
	for _, existing := range b.handlers {
		if existing.handler.ID() == h.ID() {
			return fmt.Errorf("%w: duplicate id %s", ErrInvalidHandler, h.ID())
		}
	}
	b.handlers = append(b.handlers, r)
	sort.SliceStable(b.handlers, func(i, j int) bool {
		return b.handlers[i].handler.Order() < b.handlers[j].handler.Order()
	})
	r.start()
	return nil
}

func (b *Bus) Emit(ctx context.Context, event RequestCompletionEvent) error {
	if b == nil {
		return ErrBusClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event, err := event.normalized()
	if err != nil {
		return err
	}
	handlers := b.snapshotHandlers()
	if len(handlers) == 0 {
		return ErrNoHandlers
	}
	for _, r := range handlers {
		if !r.handler.Critical() {
			continue
		}
		if err := r.submit(ctx, event, true); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrCriticalHandler, r.handler.ID(), err)
		}
	}
	for _, r := range handlers {
		if r.handler.Critical() {
			continue
		}
		_ = r.submit(context.Background(), event, false)
	}
	return nil
}

func (b *Bus) Stop(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	handlers := append([]*handlerRunner(nil), b.handlers...)
	b.mu.Unlock()

	drainCtx, cancel := context.WithTimeout(ctx, b.cfg.ShutdownDrainTimeout)
	defer cancel()
	for {
		pendingLow := 0
		for _, r := range handlers {
			if r.handler.Tier() == TierLow {
				pendingLow += r.pending()
			}
		}
		if pendingLow == 0 {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-drainCtx.Done():
			timer.Stop()
			break
		case <-timer.C:
		}
		if drainCtx.Err() != nil {
			break
		}
	}

	var wg sync.WaitGroup
	for _, r := range handlers {
		r.stop()
		wg.Add(1)
		go func(r *handlerRunner) {
			defer wg.Done()
			r.wait()
		}(r)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bus) State(eventID string, handlerID HandlerID) (StateSnapshot, bool) {
	if b == nil {
		return StateSnapshot{}, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.states[StateKey{EventID: eventID, HandlerID: handlerID}]
	return s, ok
}

func (b *Bus) snapshotHandlers() []*handlerRunner {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil
	}
	return append([]*handlerRunner(nil), b.handlers...)
}

func (b *Bus) setState(event RequestCompletionEvent, handlerID HandlerID, state HandlerState, err error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.states[StateKey{EventID: event.ID, HandlerID: handlerID}] = StateSnapshot{
		EventID:   event.ID,
		HandlerID: handlerID,
		State:     state,
		Error:     errString(err),
		UpdatedAt: b.now(),
	}
}

func (b *Bus) handlerTimeout(h Handler) time.Duration {
	if timeout := h.Timeout(); timeout > 0 {
		return timeout
	}
	return b.cfg.HandlerTimeout
}

func (b *Bus) workerCount(tier Tier) int {
	switch tier {
	case TierHigh:
		return b.cfg.HighWorkers
	case TierLow:
		return b.cfg.LowWorkers
	default:
		return b.cfg.MediumWorkers
	}
}

func (b *Bus) bufferSize(tier Tier) int {
	switch tier {
	case TierHigh:
		return b.cfg.HighBuffer
	case TierLow:
		return b.cfg.LowBuffer
	default:
		return b.cfg.MediumBuffer
	}
}

func (b *Bus) writeDLQ(event RequestCompletionEvent, h Handler, handlerErr error) {
	if b == nil || b.dlq == nil || handlerErr == nil {
		return
	}
	kind := h.DLQKind()
	if kind == "" {
		kind = dlqKindForHandler(h.ID())
	}
	if kind == "" {
		kind = dlq.EventKindMetrics
	}
	lane := dlq.Lane(h.Tier())
	if lane == "" {
		lane = dlq.LaneMed
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = b.dlq.Enqueue(ctx, dlq.Event{
		TenantID:       event.TenantID,
		ClaimID:        event.ClaimID,
		EventKind:      kind,
		Lane:           lane,
		Payload:        dlqPayload(event, h, handlerErr),
		FailureReason:  handlerErr.Error(),
		IdempotencyKey: fmt.Sprintf("async_processor:%s:%s", event.ID, h.ID()),
		SourceTable:    "async_processor_events",
		SourceID:       event.ClaimID,
	})
}

func dlqKindForHandler(id HandlerID) dlq.EventKind {
	switch id {
	case HandlerBillingPersister:
		return dlq.EventKindUsageRecord
	case HandlerAuditLogger:
		return dlq.EventKindAuditEventReplica
	case HandlerAccountHealthProbe:
		return dlq.EventKindAccountHealth
	case HandlerMetricsAggregator, HandlerReconciliationCheck:
		return dlq.EventKindMetrics
	default:
		return dlq.EventKindMetrics
	}
}
