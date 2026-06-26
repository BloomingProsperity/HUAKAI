// 跨功能集成测试：pool selector + auth.TokenProvider，经由
// AuthCredentialGate。验证 slice-1+2 评审者标记为未测的边界 ——
//「pool selector 调用 auth.TokenProvider」此前两侧都被打桩。
package pool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// AT-XFEAT-001：畸形的上游 token 导致 CredentialGate 拒绝持有它的账号；
// pool selector 故障转移到下一个凭证有效的合格账号。验证 CredentialGate ↔
// GetAccessToken 契约。
func TestATXFEAT_001_CredentialGateRejectsMalformedTokenAccount(t *testing.T) {
	refreshClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		// 返回 malformed token, 触发 auth.ErrTokenMalformed, 但不在测试里监听本地端口。
		body := `{"access_token":"not a token","refresh_token":"rt","expires_in":3600}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	authStore := newAuthMemStore()
	authCache := newAuthMemCache()
	authLock := newAuthMemLock()
	authMarker := newAuthMemMarker()
	authAudit := newAuthMemAudit()
	provider := auth.NewAntigravityTokenProvider(authStore, authAudit, authCache, authLock, authMarker, refreshClient, nil)

	expired := time.Now().Add(-1 * time.Minute)
	authStore.put(authCredFor(t, 1, 100, "old-access-token-but-malformed-after-refresh", "rt", "https://auth.invalid/token", expired))
	authStore.put(authCredFor(t, 1, 200, "valid-static-token-32chars-abcdef0123", "", "", time.Time{}))

	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(100, 1, 100, 0.10, time.Now().Add(-2*time.Hour)), // 优先级更优但凭证畸形
		snap(200, 1, 200, 0.50, time.Now().Add(-1*time.Hour)), // 优先级更差但凭证有效（静态）
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}

	gates := DefaultGateChain()
	gates.Credential = AuthCredentialGate{Provider: provider}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
		WithGateChain(gates),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1234, RequestedModel: "x"})
	if err != nil {
		t.Fatalf("selector failed; expected fail-over success: %v", err)
	}
	if res.AccountID != 200 {
		t.Fatalf("CredentialGate must reject malformed-token account 100 and fail over to 200; got %d", res.AccountID)
	}

	// 验证 gate 失败原因已写入 routing reason payload。
	var rr map[string]any
	if err := json.Unmarshal(res.RoutingReasonJSON, &rr); err != nil {
		t.Fatalf("routing reason JSON: %v", err)
	}
	excl, _ := rr["candidate_counts_by_exclusion"].(map[string]any)
	if v, ok := excl["credential"]; !ok || v.(float64) < 1 {
		t.Errorf("expected credential gate failure recorded in routing reason; got %+v", excl)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// =====================================================================
// auth 包的内存桩（为跨包访问而复制一份）
// =====================================================================

type authMemStore struct {
	accounts map[string]auth.ProviderAccountCredential
}

func newAuthMemStore() *authMemStore {
	return &authMemStore{accounts: make(map[string]auth.ProviderAccountCredential)}
}

func authStoreKey(tenantID, accountID int64) string {
	return string(rune(tenantID)) + ":" + string(rune(accountID))
}

func (s *authMemStore) put(c auth.ProviderAccountCredential) {
	s.accounts[authStoreKey(c.TenantID, c.AccountID)] = c
}

func (s *authMemStore) LoadProviderAccount(_ context.Context, tenantID, accountID int64) (auth.ProviderAccountCredential, error) {
	if c, ok := s.accounts[authStoreKey(tenantID, accountID)]; ok {
		return c, nil
	}
	return auth.ProviderAccountCredential{}, auth.ErrAccountUnavailable
}

func (s *authMemStore) SaveRefreshedCredential(_ context.Context, u auth.RefreshedCredentialUpdate) (auth.CredentialSaveResult, error) {
	cur, ok := s.accounts[authStoreKey(u.TenantID, u.AccountID)]
	if !ok {
		return auth.CredentialSaveResult{}, auth.ErrAccountUnavailable
	}
	if cur.TokenVersion != u.TokenVersion {
		return auth.CredentialSaveResult{RowsAffected: 0, Winning: &cur}, nil
	}
	cur.CredentialJSON = u.CredentialJSON
	cur.TokenVersion = u.TokenVersion + 1
	cur.RefreshTokenFingerprint = u.RefreshTokenFingerprint
	s.accounts[authStoreKey(u.TenantID, u.AccountID)] = cur
	return auth.CredentialSaveResult{RowsAffected: 1}, nil
}

type authMemCache struct {
	store map[string]string
}

func newAuthMemCache() *authMemCache { return &authMemCache{store: make(map[string]string)} }
func (c *authMemCache) Get(_ context.Context, key string) (string, error) {
	if v, ok := c.store[key]; ok {
		return v, nil
	}
	return "", errMissCache
}
func (c *authMemCache) Set(_ context.Context, key, val string, _ time.Duration) error {
	c.store[key] = val
	return nil
}

var errMissCache = errorString("cache miss")

type errorString string

func (e errorString) Error() string { return string(e) }

type authMemLock struct{ held map[string]bool }

func newAuthMemLock() *authMemLock { return &authMemLock{held: make(map[string]bool)} }
func (l *authMemLock) Acquire(_ context.Context, key string, _ time.Duration) (bool, error) {
	if l.held[key] {
		return false, nil
	}
	l.held[key] = true
	return true, nil
}
func (l *authMemLock) Release(_ context.Context, key string) error {
	delete(l.held, key)
	return nil
}

type authMemMarker struct{}

func newAuthMemMarker() *authMemMarker { return &authMemMarker{} }
func (authMemMarker) MarkTempUnschedulable(_ context.Context, _, _ int64, _ time.Time, _ string) error {
	return nil
}
func (authMemMarker) MarkOperatorAttention(_ context.Context, _, _ int64, _ string) error {
	return nil
}

type authMemAudit struct{}

func newAuthMemAudit() *authMemAudit { return &authMemAudit{} }
func (authMemAudit) WriteRefreshAudit(_ context.Context, _ *auth.RefreshAuditEntry) error {
	return nil
}

func authCredFor(t *testing.T, tenantID, accountID int64, accessToken, refreshToken, oauthEndpoint string, expiresAt time.Time) auth.ProviderAccountCredential {
	t.Helper()
	accountType := "oauth"
	if refreshToken == "" && oauthEndpoint == "" {
		accountType = "upstream_static"
		cred := map[string]any{"api_key": accessToken}
		body, _ := json.Marshal(cred)
		return auth.ProviderAccountCredential{
			TenantID: tenantID, AccountID: accountID, Provider: "antigravity",
			AccountType: accountType, Enabled: true, CredentialJSON: body, TokenVersion: 1,
		}
	}
	cred := map[string]any{
		"access_token":   accessToken,
		"refresh_token":  refreshToken,
		"expires_at":     expiresAt.Format(time.RFC3339),
		"oauth_endpoint": oauthEndpoint,
	}
	body, _ := json.Marshal(cred)
	return auth.ProviderAccountCredential{
		TenantID: tenantID, AccountID: accountID, Provider: "antigravity",
		AccountType: accountType, Enabled: true, CredentialJSON: body, TokenVersion: 1,
	}
}
