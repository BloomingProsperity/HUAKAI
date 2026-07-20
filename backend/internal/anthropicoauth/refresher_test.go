package anthropicoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestRefresherInvalidGrantRecordsAuthExpiredInRefreshTransaction(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)
	store := newMemoryRefreshStore()
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return refreshJSONResponse(http.StatusUnauthorized, map[string]any{"error": "invalid_grant"}), nil
	})}
	refresher := &Refresher{
		Store:      store,
		Endpoint:   "https://console.anthropic.test/v1/oauth/token",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}

	err := refresher.Refresh(context.Background(), 101)
	if !errors.Is(err, ErrAnthropicAuthExpired) {
		t.Fatalf("Refresh err=%v, want ErrAnthropicAuthExpired", err)
	}
	if store.failureClass != "auth_expired" {
		t.Fatalf("failureClass=%q, want auth_expired", store.failureClass)
	}
	wantCalls := []string{
		"probe:101",
		"tx_begin",
		"failure:auth_expired",
		"audit:credential_refresh_failed:auth_expired",
		"tx_commit",
	}
	if !reflect.DeepEqual(store.calls, wantCalls) {
		t.Fatalf("calls=%v want %v", store.calls, wantCalls)
	}
}

func TestRefresherUnauthorizedStatusRecordsAuthExpiredViaOutcomeClassifier(t *testing.T) {
	// 杜绝的回归：Anthropic 的 401 必须走共享的 refresh outcome
	// classifier，而不能只靠 legacy 的 invalid_grant body 解析。Mutation
	// 自检：强制让 classifier bridge 返回 unknown，就会被记成
	// non_retryable，本 test 随之变红。
	now := time.Date(2026, 5, 24, 10, 32, 0, 0, time.UTC)
	store := newMemoryRefreshStore()
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return refreshJSONResponse(http.StatusUnauthorized, map[string]any{"error": "unauthorized"}), nil
	})}
	refresher := &Refresher{
		Store:      store,
		Endpoint:   "https://console.anthropic.test/v1/oauth/token",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}

	err := refresher.Refresh(context.Background(), 101)
	if !errors.Is(err, ErrAnthropicNonRetryable) {
		t.Fatalf("Refresh err=%v, want ErrAnthropicNonRetryable", err)
	}
	if store.failureClass != "auth_expired" {
		t.Fatalf("failureClass=%q, want auth_expired", store.failureClass)
	}
	if got := store.calls[len(store.calls)-2]; got != "audit:credential_refresh_failed:auth_expired" {
		t.Fatalf("audit append call=%q, want auth_expired", got)
	}
}

func TestRefresherRateLimitUsesRetryAfterAndDoesNotRepeatRequest(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 35, 0, 0, time.UTC)
	var attempts int32
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		resp := refreshJSONResponse(http.StatusTooManyRequests, map[string]any{"error": "rate_limit_exceeded"})
		resp.Header.Set("Retry-After", "120")
		return resp, nil
	})}
	store := newMemoryRefreshStore()
	refresher := &Refresher{
		Store:      store,
		Endpoint:   "https://console.anthropic.test/v1/oauth/token",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}

	err := refresher.Refresh(context.Background(), 101)
	if !errors.Is(err, ErrAnthropicRateLimited) {
		t.Fatalf("Refresh err=%v, want ErrAnthropicRateLimited", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("refresh HTTP attempts=%d, want 1", got)
	}
	if store.failureClass != "rate_limit_exceeded" {
		t.Fatalf("failureClass=%q, want rate_limit_exceeded", store.failureClass)
	}
	wantNext := now.Add(120 * time.Second)
	if !store.nextAttempt.Equal(wantNext) {
		t.Fatalf("nextAttempt=%s want %s", store.nextAttempt, wantNext)
	}
}

func TestRefresherKeepsExistingRefreshTokenWhenResponseOmitsReplacement(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 40, 0, 0, time.UTC)
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != AnthropicRefreshTokenURL {
			t.Fatalf("refresh URL=%s, want %s", r.URL.String(), AnthropicRefreshTokenURL)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// ANT-3 D-4=B: client_id 锁定到 HUAKAI 硬编 builtin approved
		// CLI client (默认 r.ClientID 为空时落到 AnthropicPublicCLIClientID),
		// credential payload 中的 "cid-old" 不再被采用。
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-old" || body["client_id"] != AnthropicPublicCLIClientID {
			t.Fatalf("bad refresh request body: %#v", body)
		}
		return refreshJSONResponse(http.StatusOK, map[string]any{
			"access_token": "access-new",
			"expires_in":   3600,
		}), nil
	})}
	store := newMemoryRefreshStore()
	refresher := &Refresher{
		Store:      store,
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}

	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(store.savedPayload, &payload); err != nil {
		t.Fatalf("decode saved payload: %v", err)
	}
	if payload["access_token"] != "access-new" || payload["refresh_token"] != "rt-old" || payload["keep"] != "yes" {
		t.Fatalf("saved payload=%v", payload)
	}
	if !store.savedExpires.Equal(now.Add(time.Hour)) {
		t.Fatalf("savedExpires=%s want %s", store.savedExpires, now.Add(time.Hour))
	}
}

func TestRefresherAppliesClockSkewGraceToSlightlyPastExpiresAt(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 45, 0, 0, time.UTC)
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return refreshJSONResponse(http.StatusOK, map[string]any{
			"access_token":  "access-new",
			"refresh_token": "rt-new",
			"expires_at":    now.Add(-10 * time.Second).Format(time.RFC3339),
		}), nil
	})}
	store := newMemoryRefreshStore()
	refresher := &Refresher{
		Store:           store,
		Endpoint:        "https://console.anthropic.test/v1/oauth/token",
		HTTPClient:      client,
		Now:             func() time.Time { return now },
		ExpirySkewGrace: time.Minute,
	}

	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := now.Add(time.Minute)
	if !store.savedExpires.Equal(want) {
		t.Fatalf("savedExpires=%s want skew-adjusted %s", store.savedExpires, want)
	}
}

// refresh 仅信 operator-supplied 配置或硬编
// built-in profile,不接受 credential payload 里被人为篡改的 client_id 覆盖。
// 自检 mutation: 把 refresher.refreshCredential clientID 计算回退到
// firstNonEmpty(r.ClientID, mapString(cred, "client_id"), AnthropicPublicCLIClientID),
// 该 test 会读到 attacker client_id 而变红。
func TestRefresherIgnoresCredentialPayloadClientID(t *testing.T) {
	now := time.Date(2026, 5, 26, 13, 0, 0, 0, time.UTC)
	store := newMemoryRefreshStore()
	store.rec.PlaintextPayload = []byte(`{"access_token":"old","refresh_token":"rt-old","client_id":"attacker-cid"}`)
	var capturedClientID string
	client := &http.Client{Transport: refreshRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request body: %v", err)
		}
		capturedClientID = body["client_id"]
		return refreshJSONResponse(http.StatusOK, map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		}), nil
	})}
	refresher := &Refresher{
		Store:      store,
		Endpoint:   "https://console.anthropic.test/v1/oauth/token",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}

	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh err=%v", err)
	}
	if capturedClientID == "attacker-cid" {
		t.Fatalf("token endpoint saw attacker client_id %q — credential payload SSRF guard 失效", capturedClientID)
	}
	if capturedClientID != AnthropicPublicCLIClientID {
		t.Fatalf("client_id=%q want built-in approved %q", capturedClientID, AnthropicPublicCLIClientID)
	}
}

type memoryRefreshStore struct {
	rec          credentialstore.CredentialRecord
	calls        []string
	savedPayload []byte
	savedExpires time.Time
	failureClass string
	nextAttempt  time.Time
}

func newMemoryRefreshStore() *memoryRefreshStore {
	return &memoryRefreshStore{rec: credentialstore.CredentialRecord{
		ID: 44, TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		CredentialVersion: 3,
		PlaintextPayload:  []byte(`{"access_token":"access-old","refresh_token":"rt-old","client_id":"cid-old","keep":"yes"}`),
	}}
}

func (s *memoryRefreshStore) LoadForRefresh(_ context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	s.calls = append(s.calls, "probe:"+int64String(accountID))
	return cloneCredentialRecord(s.rec), nil
}

func (s *memoryRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshStore, db.DBTX) error) error {
	s.calls = append(s.calls, "tx_begin")
	tx := &memoryRefreshTx{store: s}
	err := fn(tx, tx)
	if err != nil {
		s.calls = append(s.calls, "tx_rollback")
		return err
	}
	s.calls = append(s.calls, "tx_commit")
	return nil
}

func (s *memoryRefreshStore) SaveRefreshSuccess(ctx context.Context, rec credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string) error {
	return s.WithRefreshTransaction(ctx, func(tx RefreshStore, _ db.DBTX) error {
		return tx.SaveRefreshSuccess(ctx, rec, payload, expiresAt, outcome)
	})
}

func (s *memoryRefreshStore) SaveRefreshFailure(ctx context.Context, rec credentialstore.CredentialRecord, failureClass string, nextAttempt time.Time) error {
	return s.WithRefreshTransaction(ctx, func(tx RefreshStore, _ db.DBTX) error {
		return tx.SaveRefreshFailure(ctx, rec, failureClass, nextAttempt)
	})
}

type memoryRefreshTx struct {
	store *memoryRefreshStore
}

func (tx *memoryRefreshTx) LoadForRefresh(_ context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	tx.store.calls = append(tx.store.calls, "reread:"+int64String(accountID))
	return cloneCredentialRecord(tx.store.rec), nil
}

func (tx *memoryRefreshTx) SaveRefreshSuccess(_ context.Context, _ credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string) error {
	tx.store.calls = append(tx.store.calls, "success:"+outcome)
	tx.store.calls = append(tx.store.calls, "audit:credential_refresh_succeeded:"+outcome)
	tx.store.savedPayload = append([]byte(nil), payload...)
	tx.store.savedExpires = expiresAt
	return nil
}

func (tx *memoryRefreshTx) SaveRefreshFailure(_ context.Context, _ credentialstore.CredentialRecord, failureClass string, nextAttemptAt time.Time) error {
	tx.store.calls = append(tx.store.calls, "failure:"+failureClass)
	tx.store.calls = append(tx.store.calls, "audit:credential_refresh_failed:"+failureClass)
	tx.store.failureClass = failureClass
	tx.store.nextAttempt = nextAttemptAt
	return nil
}

func (tx *memoryRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	// 锁键现以单个 text 参数传入(修复 pgx 无法把 int64 编码成 text 的 bug),故按 string 记录。
	tx.store.calls = append(tx.store.calls, "lock:"+args[0].(string))
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (tx *memoryRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *memoryRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func cloneCredentialRecord(in credentialstore.CredentialRecord) credentialstore.CredentialRecord {
	out := in
	out.PlaintextPayload = append([]byte(nil), in.PlaintextPayload...)
	return out
}

type refreshRoundTripFunc func(*http.Request) (*http.Response, error)

func (f refreshRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func refreshJSONResponse(status int, body map[string]any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

func int64String(v int64) string {
	return strconv.FormatInt(v, 10)
}
