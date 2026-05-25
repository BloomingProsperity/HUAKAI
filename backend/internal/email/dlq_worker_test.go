package email

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestAT_OBS_005_008_EmailTransientFailEnqueuesAndRetriesSuccess(t *testing.T) {
	ctx := context.Background()
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	outbox := obsdlq.NewMemoryOutbox()
	sender, err := BuildEmailSender(ctx, store, keys,
		WithOutbox(outbox),
		WithSMTPDispatch(func(context.Context, SMTPSettings, Message) error {
			return errors.New("smtp dial: connection refused")
		}),
	)
	if err != nil {
		t.Fatalf("BuildEmailSender: %v", err)
	}
	if err := sender.SendVerification(ctx, userauth.User{TenantID: 1, Email: "u@example.test"}, "tok-secret-123"); err != nil {
		t.Fatalf("transient send should enqueue and return nil: %v", err)
	}
	rows := outbox.Snapshot()
	if len(rows) != 1 || rows[0].EventType != obsdlq.EventTypeEmailRetry || rows[0].Priority != obsdlq.PriorityCritical {
		t.Fatalf("outbox rows=%+v", rows)
	}
	if strings.Contains(string(rows[0].Payload), "tok-secret-123") || obsdlq.ContainsForbiddenRawData(rows[0].Payload) {
		t.Fatalf("email retry payload leaked token/body: %s", rows[0].Payload)
	}

	var retried Message
	worker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}})
	worker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(_ context.Context, _ SMTPSettings, msg Message) error {
		retried = msg
		return nil
	}))
	processed, err := worker.RunOnce(ctx, obsdlq.PriorityCritical, "email")
	if err != nil || !processed {
		t.Fatalf("retry processed=%v err=%v", processed, err)
	}
	if retried.To != "u@example.test" || !strings.Contains(retried.HTMLBody, "tok-secret-123") {
		t.Fatalf("retry message=%+v", retried)
	}
	if outbox.Snapshot()[0].Status != obsdlq.StatusCompleted {
		t.Fatalf("status=%s want completed", outbox.Snapshot()[0].Status)
	}
}

func TestAT_OBS_005_009_EmailPermanentFailNoEnqueue(t *testing.T) {
	ctx := context.Background()
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	outbox := obsdlq.NewMemoryOutbox()
	sender, err := BuildEmailSender(ctx, store, keys,
		WithOutbox(outbox),
		WithSMTPDispatch(func(context.Context, SMTPSettings, Message) error {
			t.Fatal("dispatch must not run for invalid recipient")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildEmailSender: %v", err)
	}
	err = sender.SendVerification(ctx, userauth.User{TenantID: 1, Email: "bad-address"}, "tok")
	if !errors.Is(err, ErrEmailSettingsInvalid) {
		t.Fatalf("error=%v want ErrEmailSettingsInvalid", err)
	}
	if len(outbox.Snapshot()) != 0 {
		t.Fatalf("permanent failure enqueued rows=%+v", outbox.Snapshot())
	}
}

func TestAT_OBS_005_010_EmailDLQAfterMaxAttemptsOperatorVisible(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	outbox := obsdlq.NewMemoryOutbox(obsdlq.WithMemoryClock(func() time.Time { return now }))
	if _, err := outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		TenantID:  1,
		EventType: obsdlq.EventTypeEmailRetry,
		Priority:  obsdlq.PriorityCritical,
		Payload:   mustEmailPayload(t, ctx, keys),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	worker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}}, obsdlq.WithWorkerClock(func() time.Time { return now }))
	worker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(context.Context, SMTPSettings, Message) error {
		return errors.New("smtp tls dial: token=sk-testsecret")
	}))
	if processed, err := worker.RunOnce(ctx, obsdlq.PriorityCritical, "email"); err != nil || !processed {
		t.Fatalf("first run processed=%v err=%v", processed, err)
	}
	now = now.Add(2 * time.Second)
	if processed, err := worker.RunOnce(ctx, obsdlq.PriorityCritical, "email"); err != nil || !processed {
		t.Fatalf("second run processed=%v err=%v", processed, err)
	}
	row := outbox.Snapshot()[0]
	if row.Status != obsdlq.StatusFailedDead || len(outbox.DeadEvents()) != 1 {
		t.Fatalf("status=%s dead=%d want failed_dead/1", row.Status, len(outbox.DeadEvents()))
	}
	if obsdlq.ContainsForbiddenRawData([]byte(row.FailureReason)) || obsdlq.ContainsForbiddenRawData([]byte(outbox.DeadEvents()[0].DeadReason)) {
		t.Fatalf("DLQ reason leaked: %q / %q", row.FailureReason, outbox.DeadEvents()[0].DeadReason)
	}
}

func TestAT_OBS_005_013_EmailRestartDrainSuccess(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 13, 10, 0, 0, time.UTC)
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	outbox := obsdlq.NewMemoryOutbox(obsdlq.WithMemoryClock(func() time.Time { return now }))
	if _, err := outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		TenantID:  1,
		EventType: obsdlq.EventTypeEmailRetry,
		Priority:  obsdlq.PriorityCritical,
		Payload:   mustEmailPayload(t, ctx, keys),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	failingWorker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 5}}, obsdlq.WithWorkerClock(func() time.Time { return now }))
	failingWorker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(context.Context, SMTPSettings, Message) error {
		return errors.New("smtp dial: timeout")
	}))
	if processed, err := failingWorker.RunOnce(ctx, obsdlq.PriorityCritical, "email"); err != nil || !processed {
		t.Fatalf("first worker processed=%v err=%v", processed, err)
	}
	now = now.Add(2 * time.Second)

	var sent bool
	drainWorker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 5}}, obsdlq.WithWorkerClock(func() time.Time { return now }))
	drainWorker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(context.Context, SMTPSettings, Message) error {
		sent = true
		return nil
	}))
	if processed, err := drainWorker.RunOnce(ctx, obsdlq.PriorityCritical, "email"); err != nil || !processed {
		t.Fatalf("restart drain processed=%v err=%v", processed, err)
	}
	if !sent || outbox.Snapshot()[0].Status != obsdlq.StatusCompleted {
		t.Fatalf("sent=%v status=%s", sent, outbox.Snapshot()[0].Status)
	}
}

func mustEmailPayload(t *testing.T, ctx context.Context, keys SecretKeyProvider) []byte {
	t.Helper()
	body, err := EncodeSecret(ctx, keys, 1, "<p>retry-token</p>")
	if err != nil {
		t.Fatalf("EncodeSecret: %v", err)
	}
	raw, err := jsonMarshalRetryPayload(retryPayload{
		To:           "u@example.test",
		Subject:      "Retry",
		BodyEnvelope: body,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func jsonMarshalRetryPayload(payload retryPayload) ([]byte, error) {
	type alias retryPayload
	return json.Marshal(alias(payload))
}
