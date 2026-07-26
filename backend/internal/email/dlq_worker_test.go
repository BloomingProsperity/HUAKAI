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
	var queued retryPayload
	if err := json.Unmarshal(rows[0].Payload, &queued); err != nil {
		t.Fatalf("decode queued payload: %v", err)
	}
	if queued.To != "u@example.test" || queued.Subject == "" || !strings.HasPrefix(queued.BodyEnvelope, retryEnvelopeHexPrefix) {
		t.Fatalf("queued retry payload lost dispatch fields: %+v", queued)
	}

	var retried Message
	dispatched := false
	worker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}})
	worker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(_ context.Context, _ SMTPSettings, msg Message) error {
		dispatched = true
		retried = msg
		return nil
	}))
	processed, err := worker.RunOnce(ctx, obsdlq.PriorityCritical, "email")
	if err != nil || !processed {
		t.Fatalf("retry processed=%v err=%v", processed, err)
	}
	if !dispatched || retried.To != "u@example.test" || !strings.Contains(retried.HTMLBody, "tok-secret-123") {
		t.Fatalf("retry message=%+v", retried)
	}
	if outbox.Snapshot()[0].Status != obsdlq.StatusCompleted {
		t.Fatalf("status=%s want completed", outbox.Snapshot()[0].Status)
	}
}

func TestRetryEnvelopeTransportCannotMimicCredentialMarkers(t *testing.T) {
	// 旧版直接保存随机密文；密文若偶然含 eyJ，会被通用隐私扫描误判成 JWT 并删掉负载。
	// 新格式只含十六进制字符，既保留完整信封，也不会命中这些凭据形态。
	keys := testEmailKeys(t)
	input, err := EncodeSecret(context.Background(), keys, 1, "<p>real encrypted retry body</p>")
	if err != nil {
		t.Fatalf("EncodeSecret: %v", err)
	}
	encoded := encodeRetryEnvelope(input)
	if obsdlq.ContainsForbiddenRawData([]byte(`{"body_envelope":"` + encoded + `"}`)) {
		t.Fatalf("encoded retry envelope still matches credential markers: %q", encoded)
	}
	decoded, err := decodeRetryEnvelope(encoded)
	if err != nil || decoded != input {
		t.Fatalf("retry envelope round trip decoded=%q err=%v", decoded, err)
	}
	plaintext, err := DecodeSecret(context.Background(), keys, 1, decoded)
	if err != nil || plaintext != "<p>real encrypted retry body</p>" {
		t.Fatalf("retry envelope decrypt plaintext=%q err=%v", plaintext, err)
	}
	legacy, err := decodeRetryEnvelope(input)
	if err != nil || legacy != input {
		t.Fatalf("legacy retry envelope decoded=%q err=%v", legacy, err)
	}
	collision := encodeRetryEnvelope(secretEnvelopePrefix + `{"ciphertext":"random-eyJ-collision"}`)
	if strings.Contains(collision, "eyJ") || obsdlq.ContainsForbiddenRawData([]byte(`{"body_envelope":"`+collision+`"}`)) {
		t.Fatalf("encoded collision still matches credential markers: %q", collision)
	}
	for _, damaged := range []string{"", retryEnvelopeHexPrefix, retryEnvelopeHexPrefix + "20", retryEnvelopeHexPrefix + "xyz"} {
		if decoded, err := decodeRetryEnvelope(damaged); err == nil {
			t.Fatalf("damaged retry envelope %q decoded=%q without error", damaged, decoded)
		}
	}
}

func TestDLQHandlerRejectsDamagedTransportEnvelope(t *testing.T) {
	ctx := context.Background()
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	payload, err := json.Marshal(retryPayload{
		To:           "u@example.test",
		Subject:      "Retry",
		BodyEnvelope: retryEnvelopeHexPrefix + "20",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	outbox := obsdlq.NewMemoryOutbox()
	if _, err := outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		TenantID:  1,
		EventType: obsdlq.EventTypeEmailRetry,
		Priority:  obsdlq.PriorityCritical,
		Payload:   payload,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	dispatched := false
	worker := obsdlq.NewWorker(outbox, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}})
	worker.Register(obsdlq.EventTypeEmailRetry, NewDLQHandler(store, keys, func(context.Context, SMTPSettings, Message) error {
		dispatched = true
		return nil
	}))
	if processed, err := worker.RunOnce(ctx, obsdlq.PriorityCritical, "email"); err != nil || !processed {
		t.Fatalf("retry processed=%v err=%v", processed, err)
	}
	row := outbox.Snapshot()[0]
	if dispatched || row.Status != obsdlq.StatusFailedRetry || row.AttemptCount != 1 {
		t.Fatalf("dispatched=%v status=%s attempts=%d", dispatched, row.Status, row.AttemptCount)
	}
}

func TestDLQHandlerAcceptsLegacyEnvelope(t *testing.T) {
	ctx := context.Background()
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	body, err := EncodeSecret(ctx, keys, 1, "<p>legacy retry body</p>")
	if err != nil {
		t.Fatalf("EncodeSecret: %v", err)
	}
	payload, err := json.Marshal(retryPayload{
		To:           "u@example.test",
		Subject:      "Legacy retry",
		BodyEnvelope: body,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var retried Message
	handler := NewDLQHandler(store, keys, func(_ context.Context, _ SMTPSettings, msg Message) error {
		retried = msg
		return nil
	})
	if err := handler(ctx, obsdlq.OutboxEvent{TenantID: 1, Payload: payload}); err != nil {
		t.Fatalf("legacy retry handler: %v", err)
	}
	if retried.To != "u@example.test" || retried.HTMLBody != "<p>legacy retry body</p>" {
		t.Fatalf("legacy retry message=%+v", retried)
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
		BodyEnvelope: encodeRetryEnvelope(body),
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
