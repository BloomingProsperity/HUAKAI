package observability

import (
	"context"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

type MetricsAggregatorHandler struct {
	timeout time.Duration
	delay   time.Duration
	fail    func(eventbus.RequestCompletionEvent) error

	mu       sync.Mutex
	received int
	byModel  map[string]int
}

type MetricsAggregatorOption func(*MetricsAggregatorHandler)

func WithMetricsDelay(delay time.Duration) MetricsAggregatorOption {
	return func(h *MetricsAggregatorHandler) { h.delay = delay }
}

func WithMetricsFailure(fn func(eventbus.RequestCompletionEvent) error) MetricsAggregatorOption {
	return func(h *MetricsAggregatorHandler) { h.fail = fn }
}

func NewMetricsAggregatorHandler(timeout time.Duration, opts ...MetricsAggregatorOption) *MetricsAggregatorHandler {
	h := &MetricsAggregatorHandler{timeout: timeout, byModel: make(map[string]int)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *MetricsAggregatorHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerMetricsAggregator
}

func (h *MetricsAggregatorHandler) Tier() eventbus.Tier {
	return eventbus.TierLow
}

func (h *MetricsAggregatorHandler) Order() int {
	return 50
}

func (h *MetricsAggregatorHandler) Critical() bool {
	return false
}

func (h *MetricsAggregatorHandler) Timeout() time.Duration {
	return h.timeout
}

func (h *MetricsAggregatorHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindMetrics
}

func (h *MetricsAggregatorHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	if h == nil {
		return nil
	}
	if h.delay > 0 {
		timer := time.NewTimer(h.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if h.fail != nil {
		if err := h.fail(event); err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received++
	if h.byModel == nil {
		h.byModel = make(map[string]int)
	}
	h.byModel[event.RequestedModel]++
	return nil
}

func (h *MetricsAggregatorHandler) Count() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.received
}
