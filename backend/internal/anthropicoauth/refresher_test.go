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
		"lock:44",
		"reread:101",
		"failure:auth_expired",
		"audit:credential_refresh_failed:auth_expired",
		"tx_commit",
	}
	if !reflect.DeepEqual(store.calls, wantCalls) {
		t.Fatalf("calls=%v want %v", store.calls, wantCalls)
	}
}

func TestRefresherUnauthorizedStatusRecordsAuthExpiredViaOutcomeClassifier(t *testing.T) {
	// Regression killed: Anthropic 401 must use the shared refresh outcome
	// classifier, not only the legacy invalid_grant body parser. Mutation
	// self-check: forcing the classifier bridge to return unknown records
	// non_retryable and this test turns red.
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
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-old" || body["client_id"] != "cid-old" {
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

func (s *memoryRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
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
	tx.store.calls = append(tx.store.calls, "lock:"+int64String(args[0].(int64)))
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
