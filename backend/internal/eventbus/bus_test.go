package eventbus_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

type dlqSinkStub struct {
	mu     sync.Mutex
	events []dlq.Event
}

func (s *dlqSinkStub) Enqueue(_ context.Context, e dlq.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return int64(len(s.events)), nil
}

func (s *dlqSinkStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestBusMultiHandlerDispatchCriticalPrefixAndAsyncSuffix(t *testing.T) {
	bus := eventbus.New(eventbus.Config{HighWorkers: 2, MediumWorkers: 1, LowWorkers: 1, HandlerTimeout: time.Second})
	defer func() { _ = bus.Stop(context.Background()) }()

	var mu sync.Mutex
	var order []string
	appendOrder := func(v string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, v)
	}
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerBillingPersister,
		HandlerTier:    eventbus.TierHigh,
		HandlerOrder:   10,
		IsCritical:     true,
		HandlerTimeout: time.Second,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			appendOrder("billing")
			return nil
		},
	})
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerAuditLogger,
		HandlerTier:    eventbus.TierHigh,
		HandlerOrder:   20,
		IsCritical:     true,
		HandlerTimeout: time.Second,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			appendOrder("audit")
			return nil
		},
	})
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerAccountHealthProbe,
		HandlerTier:    eventbus.TierMed,
		HandlerOrder:   30,
		HandlerTimeout: time.Second,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			appendOrder("health")
			return nil
		},
	})

	if err := bus.Emit(context.Background(), testEvent("evt-dispatch")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	waitState(t, bus, "evt-dispatch", eventbus.HandlerAccountHealthProbe, eventbus.HandlerStateDone)
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) < 3 || got[0] != "billing" || got[1] != "audit" {
		t.Fatalf("critical prefix order=%v want billing,audit before suffix", got)
	}
}

func TestBusTimeoutAndPanicGoToDLQ(t *testing.T) {
	sink := &dlqSinkStub{}
	bus := eventbus.New(eventbus.Config{HighWorkers: 2, HighBuffer: 2, HandlerTimeout: 10 * time.Millisecond}, eventbus.WithDLQ(sink))
	defer func() { _ = bus.Stop(context.Background()) }()

	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerBillingPersister,
		HandlerTier:    eventbus.TierHigh,
		HandlerOrder:   10,
		IsCritical:     true,
		HandlerTimeout: 10 * time.Millisecond,
		HandlerDLQKind: dlq.EventKindUsageRecord,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	})
	err := bus.Emit(context.Background(), testEvent("evt-timeout"))
	if !errors.Is(err, eventbus.ErrHandlerTimeout) {
		t.Fatalf("timeout Emit err=%v want ErrHandlerTimeout", err)
	}
	if sink.count() != 1 {
		t.Fatalf("timeout DLQ count=%d want 1", sink.count())
	}
	if stopErr := bus.Stop(context.Background()); stopErr != nil {
		t.Fatalf("stop first bus: %v", stopErr)
	}

	panicSink := &dlqSinkStub{}
	panicBus := eventbus.New(eventbus.Config{HighWorkers: 1, HighBuffer: 1, HandlerTimeout: time.Second}, eventbus.WithDLQ(panicSink))
	defer func() { _ = panicBus.Stop(context.Background()) }()
	mustRegister(t, panicBus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerBillingPersister,
		HandlerTier:    eventbus.TierHigh,
		HandlerOrder:   10,
		IsCritical:     true,
		HandlerTimeout: time.Second,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			panic("boom")
		},
	})
	err = panicBus.Emit(context.Background(), testEvent("evt-panic"))
	if !errors.Is(err, eventbus.ErrHandlerPanic) {
		t.Fatalf("panic Emit err=%v want ErrHandlerPanic", err)
	}
	if panicSink.count() != 1 {
		t.Fatalf("panic DLQ count=%d want 1", panicSink.count())
	}
}

func TestBusHandlerFailureIsolationAndDLQ(t *testing.T) {
	sink := &dlqSinkStub{}
	bus := eventbus.New(eventbus.Config{
		HighWorkers:    2,
		MediumWorkers:  1,
		LowWorkers:     1,
		HighBuffer:     8,
		MediumBuffer:   8,
		LowBuffer:      8,
		HandlerTimeout: time.Second,
	}, eventbus.WithDLQ(sink))
	defer func() { _ = bus.Stop(context.Background()) }()

	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:    eventbus.HandlerBillingPersister,
		HandlerTier:  eventbus.TierHigh,
		HandlerOrder: 10,
		IsCritical:   true,
		Fn:           func(context.Context, eventbus.RequestCompletionEvent) error { return nil },
	})
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:    eventbus.HandlerAccountHealthProbe,
		HandlerTier:  eventbus.TierMed,
		HandlerOrder: 20,
		Fn:           func(context.Context, eventbus.RequestCompletionEvent) error { return nil },
	})
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerMetricsAggregator,
		HandlerTier:    eventbus.TierLow,
		HandlerOrder:   30,
		HandlerDLQKind: dlq.EventKindMetrics,
		Fn:             func(context.Context, eventbus.RequestCompletionEvent) error { return errors.New("metrics failed") },
	})

	if err := bus.Emit(context.Background(), testEvent("evt-chaos")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	waitState(t, bus, "evt-chaos", eventbus.HandlerAccountHealthProbe, eventbus.HandlerStateDone)
	waitState(t, bus, "evt-chaos", eventbus.HandlerMetricsAggregator, eventbus.HandlerStateFailed)
	if sink.count() != 1 {
		t.Fatalf("DLQ count=%d want 1", sink.count())
	}
}

func TestBusHotPathLatencyIgnoresSlowLowPriorityHandler(t *testing.T) {
	drops := 0
	bus := eventbus.New(eventbus.Config{
		HighWorkers:    2,
		LowWorkers:     1,
		HighBuffer:     16,
		LowBuffer:      2,
		HandlerTimeout: 200 * time.Millisecond,
	}, eventbus.WithDropHook(func(eventbus.DropNotice) { drops++ }))
	defer func() { _ = bus.Stop(context.Background()) }()

	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:    eventbus.HandlerBillingPersister,
		HandlerTier:  eventbus.TierHigh,
		HandlerOrder: 10,
		IsCritical:   true,
		Fn:           func(context.Context, eventbus.RequestCompletionEvent) error { return nil },
	})
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerMetricsAggregator,
		HandlerTier:    eventbus.TierLow,
		HandlerOrder:   20,
		HandlerTimeout: 200 * time.Millisecond,
		Fn: func(ctx context.Context, _ eventbus.RequestCompletionEvent) error {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	})

	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := bus.Emit(context.Background(), testEvent("evt-latency")); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("100 emits took %s; slow LOW handler leaked into hot path", elapsed)
	}
	if drops == 0 {
		t.Fatal("expected bounded LOW lane to drop oldest under backpressure")
	}
}

func mustRegister(t *testing.T, bus *eventbus.Bus, h eventbus.Handler) {
	t.Helper()
	if err := bus.Register(h); err != nil {
		t.Fatalf("Register(%s): %v", h.ID(), err)
	}
}

func waitState(t *testing.T, bus *eventbus.Bus, eventID string, handlerID eventbus.HandlerID, state eventbus.HandlerState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, ok := bus.State(eventID, handlerID)
		if ok && s.State == state {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s, ok := bus.State(eventID, handlerID); ok {
		t.Fatalf("state %s/%s=%s want %s err=%s", eventID, handlerID, s.State, state, s.Error)
	}
	t.Fatalf("state %s/%s missing want %s", eventID, handlerID, state)
}

func testEvent(id string) eventbus.RequestCompletionEvent {
	return eventbus.RequestCompletionEvent{
		ID:              id,
		TenantID:        7,
		ClaimID:         11,
		AccountID:       42,
		RequestID:       id,
		PayloadHash:     "payload-hash",
		RawBodyHash:     "raw-body-hash",
		RedactedBodyRef: "sha256:raw-body-hash",
	}
}
