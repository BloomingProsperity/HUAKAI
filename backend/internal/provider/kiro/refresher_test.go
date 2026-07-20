package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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

func TestKiroRefreshAdapterSuccessUsesAWSSSOCreateTokenShape(t *testing.T) {
	// 锁定回归：Kiro AWS SSO 刷新必须使用 operator 配置的 CreateToken 字段，
	// 并把刷新后的 access token 提升为运行时 session 凭据。
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", r.Method)
		}
		if r.URL.String() != "https://oidc.us-east-1.amazonaws.com/token" {
			t.Fatalf("token endpoint=%q", r.URL.String())
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("content-type=%q, want json", ct)
		}
		assertKiroJSONBody(t, r.Body, map[string]string{
			"clientId":     "kiro-client",
			"clientSecret": "kiro-secret",
			"grantType":    "refresh_token",
			"refreshToken": "kiro-refresh-old",
		})
		return kiroJSONResponse(http.StatusOK, `{
			"accessToken":"kiro-access-new",
			"refreshToken":"kiro-refresh-new",
			"tokenType":"Bearer",
			"expiresIn":1200
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:     "https://oidc.us-east-1.amazonaws.com/token",
		ClientID:     "kiro-client",
		ClientSecret: "kiro-secret",
		HTTPClient:   client,
		Now:          func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 51, KiroVendor, []byte(`{
		"refresh_token":"kiro-refresh-old",
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
	if got["access_token"] != "kiro-access-new" || got["session_token"] != "kiro-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "kiro-refresh-new" || got["keep"] != "yes" || got["client_id"] != "kiro-client" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
}

func TestKiroRefreshAdapterRejectsCredentialSuppliedOAuthConfig(t *testing.T) {
	t.Run("token endpoint from credential is ignored", func(t *testing.T) {
		// 锁定回归：攻击者可控的凭据 JSON 不得决定 AWS SSO refresh token POST 到
		// 何处。变异自检：若从凭据读取 oauth_token_endpoint/token_endpoint，就会调用
		// 本 HTTP client，使测试变红。
		calledURL := ""
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calledURL = r.URL.String()
			return kiroJSONResponse(http.StatusOK, `{"accessToken":"unexpected","expiresIn":60}`), nil
		})}

		_, _, err := (RefreshAdapter{
			ClientID:     "kiro-client",
			ClientSecret: "kiro-secret",
			HTTPClient:   client,
		}).RefreshForProvider(context.Background(), 52, KiroVendor, []byte(`{
			"refresh_token":"kiro-refresh-old",
			"oauth_token_endpoint":"http://169.254.169.254/latest/meta-data/iam/security-credentials"
		}`))
		if !errors.Is(err, ErrKiroSSOConfigRequired) {
			t.Fatalf("RefreshForProvider err=%v, want ErrKiroSSOConfigRequired", err)
		}
		if calledURL != "" {
			t.Fatalf("credential-supplied token endpoint was used: %s", calledURL)
		}
	})

	t.Run("client identity from credential is ignored", func(t *testing.T) {
		called := false
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return kiroJSONResponse(http.StatusOK, `{"accessToken":"unexpected","expiresIn":60}`), nil
		})}

		_, _, err := (RefreshAdapter{
			TokenURL:   "https://oidc.us-east-1.amazonaws.com/token",
			HTTPClient: client,
		}).RefreshForProvider(context.Background(), 53, KiroVendor, []byte(`{
			"refresh_token":"kiro-refresh-old",
			"client_id":"attacker-client",
			"client_secret":"attacker-secret"
		}`))
		if !errors.Is(err, ErrKiroSSOConfigRequired) {
			t.Fatalf("RefreshForProvider err=%v, want ErrKiroSSOConfigRequired", err)
		}
		if called {
			t.Fatal("refresh called HTTP client despite missing operator client identity")
		}
	})
}

func TestKiroRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// 锁定回归：Kiro 刷新失败必须映射到不同的 worker outcome。变异自检：把
	// status/body 处理压平会破坏 401、429 或触发风控的 403 中至少一个。
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       credentialworker.RefreshOutcome
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_client"}`, want: credentialworker.OutcomeAuthExpired},
		{name: "rate_limited", statusCode: http.StatusTooManyRequests, body: `{"error":"slow_down"}`, want: credentialworker.OutcomeRateLimit},
		{name: "risk_control", statusCode: http.StatusForbidden, body: `{"message":"risk control triggered"}`, want: credentialworker.OutcomeRiskControl},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return kiroJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:     "https://oidc.us-east-1.amazonaws.com/token",
				ClientID:     "kiro-client",
				ClientSecret: "kiro-secret",
				HTTPClient:   client,
			}).RefreshForProvider(context.Background(), 54, KiroVendor, []byte(`{"refresh_token":"kiro-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := credentialworker.ClassifyRefreshError(err, KiroVendor, refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if refreshErr.Outcome != string(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func TestKiroRefresherRecordsFailureOutcomeInShortPersistenceTransaction(t *testing.T) {
	// 锁定回归：provider 专属 refresher 必须在凭据事务中记录精确的失败类别，
	// 不能保存陈旧凭据，也不能把所有失败都坍缩成通用的 transient 状态。
	calls := []string{}
	store := &recordingKiroRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 91, TenantID: 1, ProviderAccountID: 42,
			Vendor: KiroVendor, AuthMode: KiroAuthModeAWSSSO, CredentialVersion: 2,
			PlaintextPayload: []byte(`{"refresh_token":"kiro-refresh-old"}`),
		},
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return kiroJSONResponse(http.StatusUnauthorized, `{"error":"invalid_client"}`), nil
	})}
	refresher := &Refresher{
		Store: store,
		Adapter: RefreshAdapter{
			TokenURL:     "https://oidc.us-east-1.amazonaws.com/token",
			ClientID:     "kiro-client",
			ClientSecret: "kiro-secret",
			HTTPClient:   client,
			Now:          func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) },
		},
		Now: func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) },
	}

	err := refresher.Refresh(context.Background(), 42)
	if !errors.Is(err, ErrKiroAuthExpired) {
		t.Fatalf("Refresh err=%v, want ErrKiroAuthExpired", err)
	}
	if got := auth.RefreshAuditOutcomeFromError(err); got != "auth_expired" {
		t.Fatalf("refresh audit outcome=%q, want auth_expired", got)
	}
	wantCalls := []string{"probe", "tx_begin", "failure:91:auth_expired"}
	if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("calls=%v, want %v", calls, wantCalls)
	}
	if store.saved {
		t.Fatal("failed refresh must not save success payload")
	}
}

func assertKiroJSONBody(t *testing.T, body io.Reader, want map[string]string) {
	t.Helper()
	var got map[string]string
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("json body %s=%q, want %q; all=%v", key, got[key], value, got)
		}
	}
}

func kiroJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type recordingKiroRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved bool
}

func (s *recordingKiroRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingKiroRefreshStore) WithRefreshTransaction(_ context.Context, fn func(RefreshStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingKiroRefreshTx{calls: s.calls, rec: s.rec, saved: &s.saved}
	return fn(tx, tx)
}

func (s *recordingKiroRefreshStore) SaveRefreshSuccess(ctx context.Context, rec credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string) error {
	return s.WithRefreshTransaction(ctx, func(tx RefreshStore, _ db.DBTX) error {
		return tx.SaveRefreshSuccess(ctx, rec, payload, expiresAt, outcome)
	})
}

func (s *recordingKiroRefreshStore) SaveRefreshFailure(ctx context.Context, rec credentialstore.CredentialRecord, failureClass string, nextAttempt time.Time) error {
	return s.WithRefreshTransaction(ctx, func(tx RefreshStore, _ db.DBTX) error {
		return tx.SaveRefreshFailure(ctx, rec, failureClass, nextAttempt)
	})
}

type recordingKiroRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	saved *bool
}

func (tx *recordingKiroRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:"+args[0].(string))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingKiroRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingKiroRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingKiroRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingKiroRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "success:"+kiroStrconvInt64(rec.ID))
	*tx.saved = true
	return nil
}

func (tx *recordingKiroRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+kiroStrconvInt64(rec.ID)+":"+failureClass)
	return nil
}

func kiroStrconvInt64(v any) string {
	n, _ := v.(int64)
	return strconv.FormatInt(n, 10)
}

// TestKiroRefreshAdapterSSRFBlocksInternalEndpoint 守 S2-054：未注入 HTTPClient 时，
// vendor refresher 必须兜底到 SSRF-protected client，使得 operator/凭据配置的、指向
// loopback/内网的 token endpoint 在拨号阶段就被拒绝——refresh token 和 client secret
// 都不得被 POST 到内网监听器。
//
// 变异自检：把 httpClient() 里改回 `return http.DefaultClient`；DefaultClient 会拨向
// 字面 127.0.0.1 监听器，handler 被命中（hit=true）、密钥外泄 → 本测试变红。
func TestKiroRefreshAdapterSSRFBlocksInternalEndpoint(t *testing.T) {
	hit := false
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"accessToken":"leaked","expiresIn":3600}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	a := RefreshAdapter{
		TokenURL:     "http://" + ln.Addr().String() + "/token", // 字面 loopback IP
		ClientID:     "cid",
		ClientSecret: "sec",
		Now:          func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) },
	}
	_, _, err = a.RefreshForProvider(context.Background(), 1, "", []byte(`{"refresh_token":"rt"}`))
	if err == nil {
		t.Fatal("refresh POST to a loopback token endpoint must be blocked by the SSRF client")
	}
	if hit {
		t.Fatal("SSRF client must NOT connect to the internal listener — refresh token + client secret would have leaked")
	}
}
