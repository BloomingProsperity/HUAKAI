package channelhealth

import (
	"context"
	"testing"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

func TestAT_OBS_005_012_ChannelHealthAlertOutboxDefaultLane(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	outbox := obsdlq.NewMemoryOutbox()
	clock := &fixedClock{now: time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock, WithAlertOutbox(outbox))

	if _, err := svc.ApplySignal(ctx, Signal{Key: testKey(), Class: SignalAccountSuspended}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	if got := store.Alerts(); len(got) != 0 {
		t.Fatalf("alert should wait for outbox worker, got %+v", got)
	}
	rows := outbox.Snapshot()
	if len(rows) != 1 || rows[0].EventType != obsdlq.EventTypeChannelAlert || rows[0].Priority != obsdlq.PriorityDefault {
		t.Fatalf("outbox rows=%+v", rows)
	}
	if obsdlq.ContainsForbiddenRawData(rows[0].Payload) {
		t.Fatalf("alert outbox leaked forbidden raw data: %s", rows[0].Payload)
	}

	worker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}})
	worker.Register(obsdlq.EventTypeChannelAlert, NewAlertDLQHandler(store))
	processed, err := worker.RunOnce(ctx, obsdlq.PriorityDefault, "channel-alert")
	if err != nil || !processed {
		t.Fatalf("worker processed=%v err=%v", processed, err)
	}
	if got := store.Alerts(); len(got) != 1 || got[0].Type != AlertBanSignal {
		t.Fatalf("alerts=%+v want ban signal", got)
	}
}
