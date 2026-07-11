package antigravity

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
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestAntigravityRefreshAdapterUsesOnlyOperatorOAuthConfig(t *testing.T) {
	// 消除的回归:由攻击者控制的凭证 JSON 不得决定刷新所用的
	// token endpoint、client ID、client secret 或 scope。
	// 变异自检:只要读取了上述任一凭证字段,就会至少发送一个攻击者值,
	// 从而使本测试变红。
	now := time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.String(); got != "https://operator.antigravity.example.test/oauth/token" {
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
		assertAntigravityQueryValue(t, form, "grant_type", "refresh_token")
		assertAntigravityQueryValue(t, form, "refresh_token", "antigravity-refresh-old")
		assertAntigravityQueryValue(t, form, "client_id", "operator-client")
		assertAntigravityQueryValue(t, form, "client_secret", "operator-secret")
		assertAntigravityQueryValue(t, form, "scope", "operator scope")
		assertNoAntigravityCredentialConfigSent(t, form)
		return antigravityJSONResponse(http.StatusOK, `{
			"access_token":"antigravity-access-new",
			"refresh_token":"antigravity-refresh-new",
			"id_token":"antigravity-id-token",
			"token_type":"Bearer",
			"expires_in":1200,
			"scope":"server scope"
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:     "https://operator.antigravity.example.test/oauth/token",
		ClientID:     "operator-client",
		ClientSecret: "operator-secret",
		Scope:        "operator scope",
		HTTPClient:   client,
		Now:          func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, AntigravityVendor, []byte(`{
		"refresh_token":"antigravity-refresh-old",
		"oauth_token_endpoint":"http://169.254.169.254/latest/meta-data/iam/security-credentials",
		"token_endpoint":"http://evil.attacker.test/token",
		"client_id":"attacker-client",
		"client_secret":"attacker-secret",
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
	if got["access_token"] != "antigravity-access-new" || got["session_token"] != "antigravity-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "antigravity-refresh-new" || got["client_id"] != "operator-client" || got["keep"] != "yes" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
	if got["scope"] != "server scope" || got["id_token"] != "antigravity-id-token" {
		t.Fatalf("token metadata=%v, want server scope and id token preserved", got)
	}
}

// TestAntigravityCLIImportPayloadFeedsExistingRefresher 证明嵌套 CLI token 经
// credentialacq 扁平化后，不需要第二套转换即可交给现有 refresher。
func TestAntigravityCLIImportPayloadFeedsExistingRefresher(t *testing.T) {
	candidates, err := credentialacq.ParseImportContent(`{
		"auth_method":"consumer",
		"token":{
			"access_token":"ag-access-old",
			"token_type":"Bearer",
			"refresh_token":"ag-refresh-old",
			"expiry":"2099-07-11T12:34:56Z"
		}
	}`, credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("解析 CLI token 失败：count=%d err=%v", len(candidates), err)
	}

	var refreshToken string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Fatalf("读取 refresh body 失败：%v", readErr)
		}
		form, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			t.Fatalf("解析 refresh body 失败：%v", parseErr)
		}
		refreshToken = form.Get("refresh_token")
		return antigravityJSONResponse(http.StatusOK, `{"access_token":"ag-access-new","expires_in":1800}`), nil
	})}
	cfg := DefaultOAuthConfig()
	cfg.HTTPClient = client
	adapter, err := RefreshAdapterFromOAuthConfig(cfg)
	if err != nil {
		t.Fatalf("构造 refresh adapter 失败：%v", err)
	}
	payload, _, err := adapter.RefreshForProvider(context.Background(), 55, AntigravityVendor, candidates[0].Payload)
	if err != nil {
		t.Fatalf("扁平 CLI token 无法刷新：%v", err)
	}
	if refreshToken != "ag-refresh-old" {
		t.Fatalf("refresh_token=%q，期望 ag-refresh-old", refreshToken)
	}
	var updated map[string]any
	if err := json.Unmarshal(payload, &updated); err != nil {
		t.Fatalf("解析刷新结果失败：%v", err)
	}
	if updated["access_token"] != "ag-access-new" || updated["session_token"] != "ag-access-new" {
		t.Fatalf("刷新结果未同步 access/session token：%s", payload)
	}
}

func TestAntigravityRefreshAdapterRejectsCredentialSuppliedTokenEndpoint(t *testing.T) {
	// 消除的回归:当运营方未配置 token_url 时,凭证里携带的 OAuth endpoint
	// 必须 fail closed(直接失败)。变异自检:一旦使用凭证里的 endpoint,
	// HTTP 客户端就会被调用,从而使本测试失败。
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return antigravityJSONResponse(http.StatusOK, `{"access_token":"unexpected","expires_in":60}`), nil
	})}

	_, _, err := (RefreshAdapter{
		ClientID:   "operator-client",
		Scope:      "operator scope",
		HTTPClient: client,
	}).RefreshForProvider(context.Background(), 43, AntigravityVendor, []byte(`{
		"refresh_token":"antigravity-refresh-old",
		"oauth_token_endpoint":"http://evil.attacker.test/token"
	}`))
	if !errors.Is(err, ErrAntigravityOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrAntigravityOAuthConfigRequired", err)
	}
	if called {
		t.Fatal("credential-supplied token endpoint was used")
	}
}

func TestAntigravityRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// 消除的回归:Antigravity 刷新失败必须保留各自不同的审计结果。
	// 变异自检:若把 status/body 的处理压平成一种,401、429 或触发风控的 403
	// 中至少有一个会失败。
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       auth.RefreshOutcome
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, want: auth.OutcomeAuthExpired},
		{name: "rate_limited", statusCode: http.StatusTooManyRequests, body: `{"error":"rate_limit_exceeded"}`, want: auth.OutcomeRateLimit},
		{name: "risk_control", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, want: auth.OutcomeRiskControl},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return antigravityJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:   "https://operator.antigravity.example.test/oauth/token",
				ClientID:   "operator-client",
				Scope:      "operator scope",
				HTTPClient: client,
			}).RefreshForProvider(context.Background(), 77, AntigravityVendor, []byte(`{"refresh_token":"antigravity-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := auth.ClassifyRefreshError(err, AntigravityVendor, refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if refreshErr.Outcome != string(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func TestAntigravityRefresherRecordsAuditOutcomeInsideRefreshLock(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
		want       string
	}{
		{name: "401 auth expired", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, wantErr: ErrAntigravityAuthExpired, want: "auth_expired"},
		{name: "429 rate limit", statusCode: http.StatusTooManyRequests, body: `{"error":"rate_limit_exceeded"}`, wantErr: ErrAntigravityRateLimited, want: "rate_limit_exceeded"},
		{name: "403 risk", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, wantErr: ErrAntigravityRiskControl, want: "risk_control_triggered"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			store := &recordingAntigravityRefreshStore{
				calls: &calls,
				rec: credentialstore.CredentialRecord{
					ID: 151, TenantID: 1, ProviderAccountID: 42,
					Vendor: AntigravityVendor, AuthMode: AntigravityAuthModeOAuth, CredentialVersion: 2,
					PlaintextPayload: []byte(`{"refresh_token":"antigravity-refresh-old"}`),
				},
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return antigravityJSONResponse(tt.statusCode, tt.body), nil
			})}
			refresher := &Refresher{
				Store: store,
				Adapter: RefreshAdapter{
					TokenURL:   "https://operator.antigravity.example.test/oauth/token",
					ClientID:   "operator-client",
					Scope:      "operator scope",
					HTTPClient: client,
					Now:        func() time.Time { return time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC) },
				},
				Now: func() time.Time { return time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC) },
			}

			err := refresher.Refresh(context.Background(), 42)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Refresh err=%v, want %v", err, tt.wantErr)
			}
			if got := auth.RefreshAuditOutcomeFromError(err); got != tt.want {
				t.Fatalf("refresh audit outcome=%q, want %q", got, tt.want)
			}
			wantCalls := []string{"probe", "tx_begin", "lock:credential_refresh:151", "reread", "failure:151:" + tt.want}
			if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
				t.Fatalf("calls=%v, want %v", calls, wantCalls)
			}
			if store.saved {
				t.Fatal("failed refresh must not save success payload")
			}
		})
	}
}

func TestAntigravityRefresherAcceptsExistingCredentialStoreMode(t *testing.T) {
	// 消除的回归:新增 antigravity/oauth 不得让 credentialstore 已经支持的
	// 现有 gemini/antigravity 凭证被搁置失效。
	calls := []string{}
	store := &recordingAntigravityRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 152, TenantID: 1, ProviderAccountID: 42,
			Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, CredentialVersion: 2,
			PlaintextPayload: []byte(`{"refresh_token":"antigravity-refresh-old"}`),
		},
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return antigravityJSONResponse(http.StatusOK, `{"access_token":"ag-access","expires_in":60}`), nil
	})}
	refresher := &Refresher{
		Store: store,
		Adapter: RefreshAdapter{
			TokenURL:   "https://operator.antigravity.example.test/oauth/token",
			ClientID:   "operator-client",
			Scope:      "operator scope",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC) },
		},
		Now: func() time.Time { return time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC) },
	}

	if err := refresher.Refresh(context.Background(), 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !store.saved {
		t.Fatal("expected successful refresh to save payload")
	}
}

func antigravityJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertNoAntigravityCredentialConfigSent(t *testing.T, form url.Values) {
	t.Helper()
	for key, bad := range map[string]string{
		"client_id":     "attacker-client",
		"client_secret": "attacker-secret",
		"scope":         "attacker-scope",
	} {
		if got := strings.TrimSpace(form.Get(key)); got == bad {
			t.Fatalf("credential-controlled %s was sent: %q", key, got)
		}
	}
}

type recordingAntigravityRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved bool
}

func (s *recordingAntigravityRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingAntigravityRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingAntigravityRefreshTx{calls: s.calls, rec: s.rec, saved: &s.saved}
	return fn(tx, tx)
}

type recordingAntigravityRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved *bool
}

func (tx *recordingAntigravityRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:"+args[0].(string))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingAntigravityRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingAntigravityRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingAntigravityRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingAntigravityRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "success:"+antigravityStrconvInt64(rec.ID))
	*tx.saved = true
	return nil
}

func (tx *recordingAntigravityRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+antigravityStrconvInt64(rec.ID)+":"+failureClass)
	return nil
}

func antigravityStrconvInt64(v any) string {
	n, _ := v.(int64)
	return strconv.FormatInt(n, 10)
}
