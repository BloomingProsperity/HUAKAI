package gemini

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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestGeminiRefreshAdapterUsesOnlyOperatorOAuthConfig(t *testing.T) {
	// Regression killed: attacker-controlled credential JSON must not decide
	// the token endpoint, client ID, or scope used for refresh. Mutation
	// self-check: reading oauth_token_endpoint/client_id/scope from credential
	// sends at least one attacker value and turns this test red.
	now := time.Date(2026, 5, 24, 14, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.String(); got != "https://operator.google.example.test/oauth/token" {
			t.Fatalf("token endpoint=%q, want operator endpoint", got)
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
		assertGeminiQueryValue(t, form, "grant_type", "refresh_token")
		assertGeminiQueryValue(t, form, "refresh_token", "gemini-refresh-old")
		assertGeminiQueryValue(t, form, "client_id", "operator-client")
		assertGeminiQueryValue(t, form, "scope", "operator scope")
		assertNoGeminiScopeFromCredential(t, form)
		return geminiJSONResponse(http.StatusOK, `{
			"access_token":"gemini-access-new",
			"refresh_token":"gemini-refresh-new",
			"id_token":"gemini-id-token",
			"token_type":"Bearer",
			"expires_in":1200,
			"scope":"server scope"
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:   "https://operator.google.example.test/oauth/token",
		ClientID:   "operator-client",
		Scope:      "operator scope",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, GeminiVendor, []byte(`{
		"refresh_token":"gemini-refresh-old",
		"oauth_token_endpoint":"http://169.254.169.254/latest/meta-data/iam/security-credentials",
		"token_endpoint":"http://evil.attacker.test/token",
		"client_id":"attacker-client",
		"scope":"credential-controlled-scope",
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
	if got["access_token"] != "gemini-access-new" || got["session_token"] != "gemini-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "gemini-refresh-new" || got["client_id"] != "operator-client" || got["keep"] != "yes" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
	if got["scope"] != "server scope" || got["id_token"] != "gemini-id-token" {
		t.Fatalf("token metadata=%v, want server scope and id token preserved", got)
	}
}

func TestGeminiRefreshAdapterPostsOperatorClientSecretForConfidentialClient(t *testing.T) {
	// Regression killed: confidential OAuth refresh must carry the operator
	// client_secret from OAuthConfig through the adapter into the token form.
	// Mutation self-check: dropping client_secret from RefreshAdapter or the
	// POST form makes this test fail before the token response is accepted.
	now := time.Date(2026, 5, 24, 14, 15, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request form: %v", err)
		}
		assertGeminiQueryValue(t, form, "grant_type", "refresh_token")
		assertGeminiQueryValue(t, form, "refresh_token", "gemini-refresh-old")
		assertGeminiQueryValue(t, form, "client_id", "operator-client")
		assertGeminiQueryValue(t, form, "client_secret", "operator-secret")
		assertGeminiQueryValue(t, form, "scope", "operator scope")
		return geminiJSONResponse(http.StatusOK, `{
			"access_token":"gemini-access-new",
			"expires_in":1200
		}`), nil
	})}

	adapter, err := RefreshAdapterFromOAuthConfig(OAuthConfig(credentialacq.OAuthClientConfig{
		AuthURL:      "https://operator.google.example.test/oauth/authorize",
		TokenURL:     "https://operator.google.example.test/oauth/token",
		ClientID:     "operator-client",
		ClientSecret: "operator-secret",
		RedirectURI:  "http://127.0.0.1:1455/auth/callback",
		Scopes:       []string{"operator", "scope"},
		HTTPClient:   client,
	}))
	if err != nil {
		t.Fatalf("RefreshAdapterFromOAuthConfig: %v", err)
	}
	adapter.Now = func() time.Time { return now }

	_, expiresAt, err := adapter.RefreshForProvider(context.Background(), 44, GeminiVendor, []byte(`{
		"refresh_token":"gemini-refresh-old"
	}`))
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if want := now.Add(1200 * time.Second); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt=%s, want %s", expiresAt, want)
	}
}

func TestGeminiRefreshAdapterRejectsCredentialSuppliedTokenEndpoint(t *testing.T) {
	// Regression killed: credential-supplied OAuth endpoints must fail closed
	// when operator token_url is absent. Mutation self-check: using the
	// credential endpoint makes the HTTP client run and this test fails.
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return geminiJSONResponse(http.StatusOK, `{"access_token":"unexpected","expires_in":60}`), nil
	})}

	_, _, err := (RefreshAdapter{
		ClientID:   "operator-client",
		Scope:      "operator scope",
		HTTPClient: client,
	}).RefreshForProvider(context.Background(), 43, GeminiVendor, []byte(`{
		"refresh_token":"gemini-refresh-old",
		"oauth_token_endpoint":"http://evil.attacker.test/token"
	}`))
	if !errors.Is(err, ErrGeminiOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrGeminiOAuthConfigRequired", err)
	}
	if called {
		t.Fatal("credential-supplied token endpoint was used")
	}
}

func TestGeminiRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// Regression killed: Gemini refresh failures must preserve distinct audit
	// outcomes. Mutation self-check: flattening status/body handling breaks at
	// least one of 401, 429, or risk-triggering 403.
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
				return geminiJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:   "https://operator.google.example.test/oauth/token",
				ClientID:   "operator-client",
				Scope:      "operator scope",
				HTTPClient: client,
			}).RefreshForProvider(context.Background(), 77, GeminiVendor, []byte(`{"refresh_token":"gemini-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := credentialworker.ClassifyRefreshError(err, GeminiVendor, refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if refreshErr.Outcome != string(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func TestGeminiRefresherRecordsAuditOutcomeInsideRefreshLock(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
		want       string
	}{
		{name: "401 auth expired", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, wantErr: ErrGeminiAuthExpired, want: "auth_expired"},
		{name: "429 rate limit", statusCode: http.StatusTooManyRequests, body: `{"error":"rate_limit_exceeded"}`, wantErr: ErrGeminiRateLimited, want: "rate_limit_exceeded"},
		{name: "403 risk", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, wantErr: ErrGeminiRiskControl, want: "risk_control_triggered"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			store := &recordingGeminiRefreshStore{
				calls: &calls,
				rec: credentialstore.CredentialRecord{
					ID: 111, TenantID: 1, ProviderAccountID: 42,
					Vendor: GeminiVendor, AuthMode: GeminiAuthModeOAuth, CredentialVersion: 2,
					PlaintextPayload: []byte(`{"refresh_token":"gemini-refresh-old"}`),
				},
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return geminiJSONResponse(tt.statusCode, tt.body), nil
			})}
			refresher := &Refresher{
				Store: store,
				Adapter: RefreshAdapter{
					TokenURL:   "https://operator.google.example.test/oauth/token",
					ClientID:   "operator-client",
					Scope:      "operator scope",
					HTTPClient: client,
					Now:        func() time.Time { return time.Date(2026, 5, 24, 14, 0, 0, 0, time.UTC) },
				},
				Now: func() time.Time { return time.Date(2026, 5, 24, 14, 0, 0, 0, time.UTC) },
			}

			err := refresher.Refresh(context.Background(), 42)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Refresh err=%v, want %v", err, tt.wantErr)
			}
			if got := auth.RefreshAuditOutcomeFromError(err); got != tt.want {
				t.Fatalf("refresh audit outcome=%q, want %q", got, tt.want)
			}
			wantCalls := []string{"probe", "tx_begin", "lock:111", "reread", "failure:111:" + tt.want}
			if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
				t.Fatalf("calls=%v, want %v", calls, wantCalls)
			}
			if store.saved {
				t.Fatal("failed refresh must not save success payload")
			}
		})
	}
}

func geminiJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type recordingGeminiRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved bool
}

func (s *recordingGeminiRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingGeminiRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingGeminiRefreshTx{calls: s.calls, rec: s.rec, saved: &s.saved}
	return fn(tx, tx)
}

type recordingGeminiRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved *bool
}

func (tx *recordingGeminiRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:"+geminiStrconvInt64(args[0]))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingGeminiRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingGeminiRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingGeminiRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingGeminiRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "success:"+geminiStrconvInt64(rec.ID))
	*tx.saved = true
	return nil
}

func (tx *recordingGeminiRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+geminiStrconvInt64(rec.ID)+":"+failureClass)
	return nil
}

func TestGeminiRefreshAdapterFallbackHTTPClientIsSSRFProtected(t *testing.T) {
	// 守 S1-003: vendor refresher 未注入 HTTPClient 时, httpClient() 兜底不能是裸
	// http.DefaultClient —— 那样 operator token endpoint 出站缺拨号层 IP 校验, 会被
	// DNS-rebind / 内网地址骗到本机或元数据服务。兜底必须是 SSRF-protected client,
	// 与 builtin mode adapter 一致。
	// Mutation 自检: 把 httpClient() 兜底改回 http.DefaultClient, CheckRedirect 变 nil
	// 且 Transport 不是 *http.Transport, 本测试断言全红。
	client := RefreshAdapter{}.httpClient()
	if client == nil {
		t.Fatal("httpClient() 返回 nil")
	}
	if client.CheckRedirect == nil {
		t.Fatal("兜底 client 缺 CheckRedirect: SSRF client 必须禁 3xx 防 client_secret 经 redirect 外泄, 裸 http.DefaultClient 不设")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("兜底 client.Transport 不是 *http.Transport (裸 http.DefaultClient 的 Transport 为 nil): %T", client.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("兜底 client transport.Proxy 必须为 nil: SSRF client 禁代理防 CONNECT 把密钥转发到内网")
	}
	injected := &http.Client{}
	if got := (RefreshAdapter{HTTPClient: injected}).httpClient(); got != injected {
		t.Fatal("注入的 HTTPClient 被兜底覆盖")
	}
}

func geminiStrconvInt64(v any) string {
	n, _ := v.(int64)
	return strconv.FormatInt(n, 10)
}
