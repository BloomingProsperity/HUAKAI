package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func seedDevicePollFlow(t *testing.T, now func() time.Time, id string) (*PostgresSessionStore, *testSessionDB, Session) {
	t.Helper()
	database := newTestSessionDB(now())
	store := withTestSessionKeys(t, NewPostgresSessionStore(database).WithNow(now))
	created, err := store.Create(context.Background(), Session{
		ID: id, TenantID: 7, ProviderAccountID: 71,
		Vendor: "openai", AuthMode: "codex_cli_oauth", Kind: FlowKindOAuth,
		Status: StatusWaitingForUser, ActorID: "admin-7", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourceOperatorConfig,
		RedactedContext:      map[string]any{"auth_type": "device_code"},
		ExpiresAt:            now().Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("创建设备授权流程: %v", err)
	}
	payload := map[string]any{
		"auth_type": "device_code", "device_code": "device-secret",
		"user_code": "USER-7", "verification_uri": "https://auth.example.test/device",
		"expires_in": 900, "interval": 5, "issued_at": now().Format(time.RFC3339Nano),
		"token_url": "https://auth.example.test/token", "client_id": "client-7",
	}
	if err := store.SetAuthPayload(context.Background(), created.ID, AuthTypeDeviceCode, payload); err != nil {
		t.Fatalf("保存设备授权载荷: %v", err)
	}
	loaded, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("读取设备授权流程: %v", err)
	}
	return store, database, loaded
}

func TestPollDeviceCodeFlowPendingReleasesLeaseAndKeepsEncryptedPayload(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	store, database, session := seedDevicePollFlow(t, func() time.Time { return now }, "flow-poll-pending")

	_, updated, err := PollDeviceCodeFlow(context.Background(), store, session,
		func(context.Context, Session) (CredentialCandidate, error) {
			return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: 7 * time.Second}
		}, nil, "admin-7", "req-pending")
	if !errors.Is(err, ErrDevicePollPending) {
		t.Fatalf("err=%v want ErrDevicePollPending", err)
	}
	if got := DevicePollRetryAfter(err); got != 7*time.Second {
		t.Fatalf("retry_after=%s want 7s", got)
	}
	if updated.Status != StatusWaitingForUser || updated.ErrorClass != "authorization_pending" {
		t.Fatalf("等待状态不完整: %+v", updated)
	}
	if stringField(updated.DeviceCodePayload, "device_code") != "device-secret" {
		t.Fatal("等待状态丢失内存中的设备授权载荷")
	}

	database.mu.Lock()
	raw := database.rows[session.ID]
	database.mu.Unlock()
	if len(raw.DeviceCodePayload) != 0 {
		t.Fatalf("数据库出现明文设备授权载荷: %v", raw.DeviceCodePayload)
	}
	if len(raw.EncryptedPKCEVerifier) == 0 || len(raw.NonceHash) == 0 || len(raw.StateHash) != 0 {
		t.Fatalf("等待状态密文或租约清理不正确: ciphertext=%d metadata=%d lease=%d", len(raw.EncryptedPKCEVerifier), len(raw.NonceHash), len(raw.StateHash))
	}
}

func TestPollDeviceCodeFlowSuccessStagesCandidateAndRecoversWithoutRepoll(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 10, 0, 0, time.UTC)
	store, database, session := seedDevicePollFlow(t, func() time.Time { return now }, "flow-poll-success")
	wantPayload := []byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_in":3600}`)
	var calls atomic.Int32

	candidate, validated, err := PollDeviceCodeFlow(context.Background(), store, session,
		func(_ context.Context, current Session) (CredentialCandidate, error) {
			calls.Add(1)
			return CredentialCandidate{TenantID: current.TenantID, ProviderAccountID: current.ProviderAccountID, Vendor: current.Vendor, AuthMode: current.AuthMode, Payload: wantPayload}, nil
		}, nil, "admin-7", "req-success")
	if err != nil {
		t.Fatalf("首次轮询: %v", err)
	}
	if validated.Status != StatusValidated || calls.Load() != 1 {
		t.Fatalf("validated=%s calls=%d", validated.Status, calls.Load())
	}
	assertJSONEqual(t, candidate.Payload, wantPayload)

	database.mu.Lock()
	raw := database.rows[session.ID]
	database.mu.Unlock()
	if len(raw.DeviceCodePayload) != 0 || bytes.Contains(raw.EncryptedPKCEVerifier, []byte("access-secret")) || len(raw.EncryptedPKCEVerifier) == 0 {
		t.Fatalf("成功候选没有只以密文暂存: plaintext=%v ciphertext=%d", raw.DeviceCodePayload, len(raw.EncryptedPKCEVerifier))
	}

	recovered, recoveredSession, err := PollDeviceCodeFlow(context.Background(), store, validated,
		func(context.Context, Session) (CredentialCandidate, error) {
			calls.Add(1)
			return CredentialCandidate{}, errors.New("已验证流程不应再次访问上游")
		}, nil, "admin-7", "req-recover")
	if err != nil {
		t.Fatalf("恢复已验证候选: %v", err)
	}
	if recoveredSession.Status != StatusValidated || calls.Load() != 1 {
		t.Fatalf("恢复状态=%s calls=%d", recoveredSession.Status, calls.Load())
	}
	assertJSONEqual(t, recovered.Payload, wantPayload)
}

func TestPollDeviceCodeFlowConcurrentClaimAllowsOnlyOneUpstreamCall(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 20, 0, 0, time.UTC)
	store, _, session := seedDevicePollFlow(t, func() time.Time { return now }, "flow-poll-concurrent")
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := PollDeviceCodeFlow(context.Background(), store, session,
			func(context.Context, Session) (CredentialCandidate, error) {
				close(entered)
				<-release
				return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: 5 * time.Second}
			}, nil, "admin-7", "req-first")
		firstDone <- err
	}()
	<-entered

	var secondCalls atomic.Int32
	_, _, secondErr := PollDeviceCodeFlow(context.Background(), store, session,
		func(context.Context, Session) (CredentialCandidate, error) {
			secondCalls.Add(1)
			return CredentialCandidate{}, nil
		}, nil, "admin-7", "req-second")
	if !errors.Is(secondErr, ErrDevicePollInProgress) || secondCalls.Load() != 0 {
		t.Fatalf("second err=%v calls=%d", secondErr, secondCalls.Load())
	}
	close(release)
	if err := <-firstDone; !errors.Is(err, ErrDevicePollPending) {
		t.Fatalf("first err=%v want pending", err)
	}
}

func TestPollDeviceCodeFlowReclaimsStaleLeaseAfterProcessLoss(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 25, 0, 0, time.UTC)
	store, database, session := seedDevicePollFlow(t, func() time.Time { return now }, "flow-poll-stale")
	if _, err := store.claimDevicePoll(context.Background(), session.ID, HashIdempotencyKey("dead-owner"), DefaultDevicePollLease); err != nil {
		t.Fatalf("领取首个租约: %v", err)
	}
	var calls atomic.Int32
	_, _, err := PollDeviceCodeFlow(context.Background(), store, session,
		func(context.Context, Session) (CredentialCandidate, error) {
			calls.Add(1)
			return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: 5 * time.Second}
		}, nil, "admin-7", "req-before-stale")
	if !errors.Is(err, ErrDevicePollInProgress) || calls.Load() != 0 {
		t.Fatalf("未过期租约 err=%v calls=%d", err, calls.Load())
	}

	now = now.Add(DefaultDevicePollLease + time.Second)
	database.mu.Lock()
	database.now = now
	database.mu.Unlock()
	_, updated, err := PollDeviceCodeFlow(context.Background(), store, session,
		func(context.Context, Session) (CredentialCandidate, error) {
			calls.Add(1)
			return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: 5 * time.Second}
		}, nil, "admin-7", "req-after-stale")
	if !errors.Is(err, ErrDevicePollPending) || calls.Load() != 1 || updated.Status != StatusWaitingForUser {
		t.Fatalf("过期租约接管 err=%v calls=%d status=%s", err, calls.Load(), updated.Status)
	}
}

func TestPollDeviceCodeFlowErrorClassificationControlsTerminalCleanup(t *testing.T) {
	cases := []struct {
		name       string
		pollErr    error
		wantErr    error
		wantStatus FlowStatus
		wantSecret bool
	}{
		{name: "临时错误", pollErr: errors.New("network unavailable"), wantErr: ErrDevicePollTransient, wantStatus: StatusWaitingForUser, wantSecret: true},
		{name: "授权过期", pollErr: ErrFlowExpired, wantErr: ErrFlowExpired, wantStatus: StatusExpired},
		{name: "用户拒绝", pollErr: ErrDeviceAccessDenied, wantErr: ErrDeviceAccessDenied, wantStatus: StatusFailed},
		{name: "响应不合法", pollErr: ErrInvalidTokenShape, wantErr: ErrInvalidTokenShape, wantStatus: StatusFailed},
		{name: "换码结果不确定", pollErr: ErrDeviceExchangeAmbiguous, wantErr: ErrDeviceExchangeAmbiguous, wantStatus: StatusFailed},
		{name: "请求取消", pollErr: context.Canceled, wantErr: context.Canceled, wantStatus: StatusWaitingForUser, wantSecret: true},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 7, 19, 8, 30+index, 0, 0, time.UTC)
			store, database, session := seedDevicePollFlow(t, func() time.Time { return now }, "flow-error-"+tc.name)
			_, updated, err := PollDeviceCodeFlow(context.Background(), store, session,
				func(context.Context, Session) (CredentialCandidate, error) { return CredentialCandidate{}, tc.pollErr },
				nil, "admin-7", "req-error")
			if !errors.Is(err, tc.wantErr) || updated.Status != tc.wantStatus {
				t.Fatalf("err=%v status=%s want err=%v status=%s", err, updated.Status, tc.wantErr, tc.wantStatus)
			}
			database.mu.Lock()
			raw := database.rows[session.ID]
			database.mu.Unlock()
			hasSecret := len(raw.EncryptedPKCEVerifier) > 0 || len(raw.NonceHash) > 0
			if hasSecret != tc.wantSecret {
				t.Fatalf("短期授权材料保留=%v want %v", hasSecret, tc.wantSecret)
			}
		})
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("解析 got JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("解析 want JSON: %v", err)
	}
	gotRaw, _ := json.Marshal(gotValue)
	wantRaw, _ := json.Marshal(wantValue)
	if !bytes.Equal(gotRaw, wantRaw) {
		t.Fatalf("JSON=%s want %s", gotRaw, wantRaw)
	}
}
