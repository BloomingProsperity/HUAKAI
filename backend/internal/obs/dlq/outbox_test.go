package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestAT_OBS_005_001_EnqueueDequeuePriorityOrdering(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "default", PriorityDefault)
	mustEnqueue(t, box, "high", PriorityHigh)
	mustEnqueue(t, box, "critical", PriorityCritical)

	got := dequeueTypes(t, box, PriorityAny, 3, now)
	want := []string{"critical", "high", "default"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("priority order=%v want %v", got, want)
		}
	}
}

func TestAT_OBS_005_002_CriticalPriorityPreemptsDefault(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 5, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "default", PriorityDefault)
	mustEnqueue(t, box, "critical", PriorityCritical)

	ev, ok, err := box.Dequeue(context.Background(), DequeueOptions{Priority: PriorityAny, Now: now})
	if err != nil || !ok {
		t.Fatalf("dequeue err=%v ok=%v", err, ok)
	}
	if ev.EventType != "critical" {
		t.Fatalf("first event=%s want critical", ev.EventType)
	}
}

func TestAT_OBS_005_003_ConcurrentLaneProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 5, 17, 12, 10, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "default", PriorityDefault)

	worker := NewWorker(box, WorkerConfig{IdleSleep: time.Millisecond, RetryPolicy: RetryPolicy{MaxAttempts: 2}}, WithWorkerClock(func() time.Time { return now }))
	defaultStarted := make(chan struct{})
	releaseDefault := make(chan struct{})
	criticalDone := make(chan struct{})
	worker.Register("default", func(context.Context, OutboxEvent) error {
		close(defaultStarted)
		<-releaseDefault
		return nil
	})
	worker.Register("critical", func(context.Context, OutboxEvent) error {
		close(criticalDone)
		return nil
	})
	worker.Start(ctx)
	defer func() {
		close(releaseDefault)
		_ = worker.Stop(context.Background())
	}()

	select {
	case <-defaultStarted:
	case <-time.After(time.Second):
		t.Fatal("default lane did not start")
	}
	mustEnqueue(t, box, "critical", PriorityCritical)
	select {
	case <-criticalDone:
	case <-time.After(time.Second):
		t.Fatal("critical lane was blocked by default lane")
	}
}

func TestAT_OBS_005_004_RetryBackoffSchedule(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 12, 20, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "critical", PriorityCritical)
	worker := NewWorker(box, WorkerConfig{RetryPolicy: RetryPolicy{MaxAttempts: 5}}, WithWorkerClock(func() time.Time { return now }))
	worker.Register("critical", func(context.Context, OutboxEvent) error { return errors.New("smtp dial: token=sk-testsecret") })

	processed, err := worker.RunOnce(ctx, PriorityCritical, "test")
	if err != nil || !processed {
		t.Fatalf("run once processed=%v err=%v", processed, err)
	}
	ev := box.Snapshot()[0]
	if ev.Status != StatusFailedRetry || ev.AttemptCount != 1 {
		t.Fatalf("event status=%s attempts=%d want failed_retry/1", ev.Status, ev.AttemptCount)
	}
	if !ev.NextRetryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("next_retry_at=%s want %s", ev.NextRetryAt, now.Add(time.Second))
	}
	if ContainsForbiddenRawData([]byte(ev.FailureReason)) {
		t.Fatalf("failure reason not redacted: %q", ev.FailureReason)
	}
}

func TestAT_OBS_005_005_DLQAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 12, 25, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "critical", PriorityCritical)
	worker := NewWorker(box, WorkerConfig{RetryPolicy: RetryPolicy{MaxAttempts: 2}}, WithWorkerClock(func() time.Time { return now }))
	worker.Register("critical", func(context.Context, OutboxEvent) error { return errors.New("upstream timeout") })

	processed, err := worker.RunOnce(ctx, PriorityCritical, "test")
	if err != nil || !processed {
		t.Fatalf("first run processed=%v err=%v", processed, err)
	}
	now = now.Add(2 * time.Second)
	processed, err = worker.RunOnce(ctx, PriorityCritical, "test")
	if err != nil || !processed {
		t.Fatalf("second run processed=%v err=%v", processed, err)
	}
	ev := box.Snapshot()[0]
	if ev.Status != StatusFailedDead || len(box.DeadEvents()) != 1 {
		t.Fatalf("status=%s dead=%d want failed_dead/1", ev.Status, len(box.DeadEvents()))
	}
}

func TestAT_OBS_005_006_AdvisoryLockCrossWorkerEquivalent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 12, 30, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	mustEnqueue(t, box, "critical", PriorityCritical)
	worker := NewWorker(box, WorkerConfig{RetryPolicy: RetryPolicy{MaxAttempts: 2}}, WithWorkerClock(func() time.Time { return now }))
	var calls atomic.Int32
	worker.Register("critical", func(context.Context, OutboxEvent) error {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil
	})

	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _ = worker.RunOnce(ctx, PriorityCritical, "race")
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	if calls.Load() != 1 {
		t.Fatalf("handler calls=%d want 1", calls.Load())
	}
	if box.Snapshot()[0].Status != StatusCompleted {
		t.Fatalf("status=%s want completed", box.Snapshot()[0].Status)
	}
}

func TestAT_OBS_005_007_DLQRowRedaction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 12, 35, 0, 0, time.UTC)
	box := NewMemoryOutbox(WithMemoryClock(func() time.Time { return now }))
	payload := json.RawMessage(`{"token":"sk-testsecret","prompt":"raw user prompt","credential_version":3,"safe":"ok"}`)
	ev, err := box.Enqueue(ctx, OutboxEvent{TenantID: 1, EventType: "critical", Priority: PriorityCritical, Payload: payload})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := box.MarkFailedDead(ctx, ev.ID, "", "token=sk-testsecret prompt=raw"); err != nil {
		t.Fatalf("mark dead: %v", err)
	}
	got := box.Snapshot()[0]
	if ContainsForbiddenRawData(got.Payload) || ContainsForbiddenRawData([]byte(got.FailureReason)) {
		t.Fatalf("dead event leaked raw data payload=%s reason=%q", got.Payload, got.FailureReason)
	}
	if !json.Valid(got.Payload) || len(box.DeadEvents()) != 1 {
		t.Fatalf("bad DLQ row payload=%s dead=%d", got.Payload, len(box.DeadEvents()))
	}
}

func TestAT_OBS_005_012_AlertSinkPriorityDefault(t *testing.T) {
	ctx := context.Background()
	box := NewMemoryOutbox()
	ev, err := box.Enqueue(ctx, OutboxEvent{
		TenantID:  1,
		EventType: EventTypeChannelAlert,
		Priority:  PriorityDefault,
		Payload:   json.RawMessage(`{"alert_type":"ban_signal","credential_version":3}`),
	})
	if err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}
	if ev.Priority != PriorityDefault {
		t.Fatalf("alert priority=%s want default", ev.Priority)
	}
	if ContainsForbiddenRawData(ev.Payload) {
		t.Fatalf("alert payload redaction over/under applied: %s", ev.Payload)
	}
}

func mustEnqueue(t *testing.T, box *MemoryOutbox, eventType string, priority Priority) {
	t.Helper()
	if _, err := box.Enqueue(context.Background(), OutboxEvent{
		TenantID:  1,
		EventType: eventType,
		Priority:  priority,
		Payload:   json.RawMessage(`{"safe":"ok"}`),
	}); err != nil {
		t.Fatalf("enqueue %s: %v", eventType, err)
	}
}

func dequeueTypes(t *testing.T, box *MemoryOutbox, priority Priority, count int, now time.Time) []string {
	t.Helper()
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		ev, ok, err := box.Dequeue(context.Background(), DequeueOptions{Priority: priority, Now: now})
		if err != nil || !ok {
			t.Fatalf("dequeue %d err=%v ok=%v", i, err, ok)
		}
		out = append(out, ev.EventType)
	}
	return out
}
