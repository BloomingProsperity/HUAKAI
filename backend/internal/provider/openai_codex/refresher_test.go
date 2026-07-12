package openai_codex

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

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestOpenAICodexRefreshAdapterUsesOnlyOperatorOAuthConfig(t *testing.T) {
	// 防回归：攻击者可控的凭据 JSON 绝不能决定刷新所用的 token endpoint、
	// client ID 或 scope。变异自检：若从凭据中读取
	// oauth_token_endpoint/client_id/scope，就会发出至少一个攻击者提供的值，
	// 使本测试变红。
	now := time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC)
	calledURL := ""
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calledURL = r.URL.String()
		if calledURL != "https://operator.openai.example.test/oauth/token" {
			t.Fatalf("token endpoint=%q, want operator endpoint", calledURL)
		}
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
		assertQueryValue(t, form, "refresh_token", "codex-refresh-old")
		assertQueryValue(t, form, "client_id", "operator-client")
		assertQueryValue(t, form, "scope", "operator scope")
		return openAICodexJSONResponse(http.StatusOK, `{
			"access_token":"codex-access-new",
			"refresh_token":"codex-refresh-new",
			"token_type":"Bearer",
			"expires_in":1200,
			"scope":"server scope"
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:   "https://operator.openai.example.test/oauth/token",
		ClientID:   "operator-client",
		Scope:      "operator scope",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, OpenAICodexClassifierVendor, []byte(`{
		"refresh_token":"codex-refresh-old",
		"oauth_token_endpoint":"http://169.254.169.254/latest/meta-data/iam/security-credentials",
		"token_endpoint":"http://evil.attacker.test/token",
		"client_id":"attacker-client",
		"scope":"attacker-scope",
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
	if got["access_token"] != "codex-access-new" || got["session_token"] != "codex-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "codex-refresh-new" || got["client_id"] != "operator-client" || got["keep"] != "yes" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
	if got["scope"] != "server scope" {
		t.Fatalf("scope=%q, want server returned scope after operator-scoped request", got["scope"])
	}
}

func TestOpenAICodexRefreshAdapterRejectsCredentialSuppliedTokenEndpoint(t *testing.T) {
	// 防回归：当缺少运维方提供的 token_url 时，凭据自带的 OAuth endpoint 必须
	// fail-closed（拒绝放行）。变异自检：若使用凭据里的 endpoint，HTTP client
	// 就会发起请求，本测试随即失败。
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return openAICodexJSONResponse(http.StatusOK, `{"access_token":"unexpected","expires_in":60}`), nil
	})}

	_, _, err := (RefreshAdapter{
		ClientID:   "operator-client",
		HTTPClient: client,
	}).RefreshForProvider(context.Background(), 43, OpenAICodexClassifierVendor, []byte(`{
		"refresh_token":"codex-refresh-old",
		"oauth_token_endpoint":"http://evil.attacker.test/token"
	}`))
	if !errors.Is(err, ErrOpenAICodexOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrOpenAICodexOAuthConfigRequired", err)
	}
	if called {
		t.Fatal("credential-supplied token endpoint was used")
	}
}

func TestOpenAICodexRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// 防回归：OpenAI Codex 的刷新失败必须保留各自不同的审计结果。变异自检：
	// 若把 status/body 的处理逻辑压平合并，401、429 或触发风控的 403 中至少
	// 会有一个判定出错。
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       credentialworker.RefreshOutcome
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, want: credentialworker.OutcomeAuthExpired},
		{name: "rate_limited", statusCode: http.StatusTooManyRequests, body: `{"error":"rate_limit_exceeded"}`, want: credentialworker.OutcomeRateLimit},
		{name: "risk_control", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, want: credentialworker.OutcomeRiskControl},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return openAICodexJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:   "https://operator.openai.example.test/oauth/token",
				ClientID:   "operator-client",
				HTTPClient: client,
			}).RefreshForProvider(context.Background(), 77, OpenAICodexClassifierVendor, []byte(`{"refresh_token":"codex-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := credentialworker.ClassifyRefreshError(err, OpenAICodexClassifierVendor, refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if refreshErr.Outcome != string(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func TestOpenAICodexRefreshErrorBodyIsCapped(t *testing.T) {
	// 防回归：刷新审计文本应携带足够的脱敏上下文以供分类，同时不能嵌入
	// 过大的上游响应 body。
	largeBody := `{"message":"risk control triggered ` + strings.Repeat("x", 8192) + `"}`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return openAICodexJSONResponse(http.StatusForbidden, largeBody), nil
	})}
	_, _, err := (RefreshAdapter{
		TokenURL:   "https://operator.openai.example.test/oauth/token",
		ClientID:   "operator-client",
		HTTPClient: client,
	}).RefreshForProvider(context.Background(), 78, OpenAICodexClassifierVendor, []byte(`{"refresh_token":"codex-refresh-old"}`))
	if err == nil {
		t.Fatal("RefreshForProvider err=nil, want classified error")
	}
	if len(err.Error()) > 1400 {
		t.Fatalf("refresh error length=%d, want capped audit-sized message", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "risk control triggered") {
		t.Fatalf("refresh error lost risk context: %q", err.Error())
	}
}

func TestOpenAICodexRefresherRecordsAuditOutcomeInsideRefreshLock(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
		want       string
	}{
		{name: "401 auth expired", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, wantErr: ErrOpenAICodexAuthExpired, want: "auth_expired"},
		{name: "429 rate limit", statusCode: http.StatusTooManyRequests, body: `{"error":"rate_limit_exceeded"}`, wantErr: ErrOpenAICodexRateLimited, want: "rate_limit_exceeded"},
		{name: "403 risk", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, wantErr: ErrOpenAICodexRiskControl, want: "risk_control_triggered"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			store := &recordingOpenAICodexRefreshStore{
				calls: &calls,
				rec: credentialstore.CredentialRecord{
					ID: 101, TenantID: 1, ProviderAccountID: 42,
					Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, CredentialVersion: 2,
					PlaintextPayload: []byte(`{"refresh_token":"codex-refresh-old"}`),
				},
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return openAICodexJSONResponse(tt.statusCode, tt.body), nil
			})}
			refresher := &Refresher{
				Store: store,
				Adapter: RefreshAdapter{
					TokenURL:   "https://operator.openai.example.test/oauth/token",
					ClientID:   "operator-client",
					HTTPClient: client,
					Now:        func() time.Time { return time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC) },
				},
				Now: func() time.Time { return time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC) },
			}

			err := refresher.Refresh(context.Background(), 42)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Refresh err=%v, want %v", err, tt.wantErr)
			}
			if got := auth.RefreshAuditOutcomeFromError(err); got != tt.want {
				t.Fatalf("refresh audit outcome=%q, want %q", got, tt.want)
			}
			wantCalls := []string{"probe", "tx_begin", "lock:credential_refresh:101", "reread", "failure:101:" + tt.want}
			if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
				t.Fatalf("calls=%v, want %v", calls, wantCalls)
			}
			if store.saved {
				t.Fatal("failed refresh must not save success payload")
			}
		})
	}
}

func openAICodexJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertQueryValue(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("query %s=%q, want %q; all=%v", key, got, want, q)
	}
}

type recordingOpenAICodexRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved bool
}

func (s *recordingOpenAICodexRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingOpenAICodexRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingOpenAICodexRefreshTx{calls: s.calls, rec: s.rec, saved: &s.saved}
	return fn(tx, tx)
}

type recordingOpenAICodexRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved *bool
}

func (tx *recordingOpenAICodexRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:" + args[0].(string))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingOpenAICodexRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingOpenAICodexRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingOpenAICodexRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingOpenAICodexRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "success:"+strconvInt64(rec.ID))
	*tx.saved = true
	return nil
}

func (tx *recordingOpenAICodexRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+strconvInt64(rec.ID)+":"+failureClass)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func strconvInt64(v any) string {
	n, _ := v.(int64)
	return strconv.FormatInt(n, 10)
}
