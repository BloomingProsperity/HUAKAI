package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestCursorRefreshAdapterSuccessMergesTokenAndPreservesConfig(t *testing.T) {
	// Regression killed: refreshed Cursor access material must become the
	// runtime session token while preserving operator-supplied OAuth config.
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q, want form", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request form: %v", err)
		}
		assertQueryValue(t, form, "grant_type", "refresh_token")
		assertQueryValue(t, form, "refresh_token", "cursor-refresh-old")
		assertQueryValue(t, form, "client_id", "cursor-client")
		return cursorJSONResponse(http.StatusOK, `{
			"access_token":"cursor-access-new",
			"refresh_token":"cursor-refresh-new",
			"token_type":"Bearer",
			"expires_in":1200,
			"scope":"openid offline_access"
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:   "https://cursor-oauth.example.test/token",
		ClientID:   "cursor-client",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, "cursor", []byte(`{
		"refresh_token":"cursor-refresh-old",
		"oauth_token_endpoint":"https://cursor-oauth.example.test/token",
		"client_id":"cursor-client",
		"keep":"yes"
	}`))
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if want := now.Add(1200 * time.Second); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt=%s, want %s", expiresAt, want)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal refreshed credential: %v", err)
	}
	if got["access_token"] != "cursor-access-new" || got["session_token"] != "cursor-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "cursor-refresh-new" || got["keep"] != "yes" || got["client_id"] != "cursor-client" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
}

func TestCursorRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// Regression killed: Cursor refresh failures must map to distinct worker
	// outcomes. Mutation self-check: removing body parsing makes the 400
	// invalid_grant case become unknown; flattening status handling breaks at
	// least one of 401, 429, or 5xx.
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       credentialworker.RefreshOutcome
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"bad_credentials"}`, want: credentialworker.OutcomeAuthExpired},
		{name: "invalid_grant_body", statusCode: http.StatusBadRequest, body: `{"error":"invalid_grant"}`, want: credentialworker.OutcomeAuthExpired},
		{name: "rate_limited", statusCode: http.StatusTooManyRequests, body: `{"error":"slow_down"}`, want: credentialworker.OutcomeRateLimit},
		{name: "server_error", statusCode: http.StatusBadGateway, body: `{"error":"upstream_unavailable"}`, want: credentialworker.OutcomeTransientError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return cursorJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:   "https://cursor-oauth.example.test/token",
				ClientID:   "cursor-client",
				HTTPClient: client,
			}).RefreshForProvider(context.Background(), 77, "cursor", []byte(`{"refresh_token":"cursor-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := credentialworker.ClassifyRefreshError(err, "cursor", refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if RefreshOutcome(refreshErr.Outcome) != RefreshOutcome(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func TestCursorRefresherRecordsFailureOutcomeInsideRefreshLock(t *testing.T) {
	// Regression killed: provider-specific refresher must record the precise
	// failure class in the credential transaction, not save a stale credential
	// or collapse every failure to a generic transient state.
	calls := []string{}
	store := &recordingCursorRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 71, TenantID: 1, ProviderAccountID: 42,
			Vendor: "cursor", AuthMode: "oauth", CredentialVersion: 2,
			PlaintextPayload: []byte(`{"refresh_token":"cursor-refresh-old"}`),
		},
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return cursorJSONResponse(http.StatusTooManyRequests, `{"error":"slow_down"}`), nil
	})}
	refresher := &Refresher{
		Store: store,
		Adapter: RefreshAdapter{
			TokenURL:   "https://cursor-oauth.example.test/token",
			ClientID:   "cursor-client",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC) },
		},
		Now: func() time.Time { return time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC) },
	}

	err := refresher.Refresh(context.Background(), 42)
	if err == nil {
		t.Fatal("Refresh err=nil, want rate limit")
	}
	wantCalls := []string{"probe", "tx_begin", "lock:71", "reread", "failure:71:rate_limit_exceeded"}
	if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("calls=%v, want %v", calls, wantCalls)
	}
	if store.saved {
		t.Fatal("rate-limited refresh must not save success payload")
	}
}

func cursorJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type recordingCursorRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved bool
}

func (s *recordingCursorRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingCursorRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingCursorRefreshTx{calls: s.calls, rec: s.rec, saved: &s.saved}
	return fn(tx, tx)
}

type recordingCursorRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved *bool
}

func (tx *recordingCursorRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:"+strconvInt64(args[0]))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingCursorRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingCursorRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingCursorRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingCursorRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "success:"+strconvInt64(rec.ID))
	*tx.saved = true
	return nil
}

func (tx *recordingCursorRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+strconvInt64(rec.ID)+":"+failureClass)
	return nil
}

func strconvInt64(v any) string {
	n, _ := v.(int64)
	return strconv.FormatInt(n, 10)
}
