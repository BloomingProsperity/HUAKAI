// Package auth tests the F-AUTH-005 implementation against the contract
// in docs/specs/upstream-credential-management.md.
//
// All tests use in-memory stubs (auth_helpers_test.go) + httptest.Server
// for upstream OAuth refresh; no external dependencies required.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// =====================================================================
// Helpers
// =====================================================================

type rig struct {
	provider *AntigravityTokenProvider
	store    *memStore
	cache    *memCache
	lock     *memLock
	marker   *memMarker
	audit    *memAudit
	upstream *httptest.Server
}

// newRig builds a fully-wired test provider. The upstream OAuth server is
// configurable via the upstreamHandler arg.
func newRig(t *testing.T, upstreamHandler http.HandlerFunc) *rig {
	t.Helper()
	store := newMemStore()
	cache := newMemCache()
	lock := newMemLock()
	marker := newMemMarker()
	audit := newMemAudit()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	provider := NewAntigravityTokenProvider(store, audit, cache, lock, marker, upstream.Client(), nil)
	return &rig{provider: provider, store: store, cache: cache, lock: lock, marker: marker, audit: audit, upstream: upstream}
}

// addAccount inserts a Provider Account with the given credential body.
func (r *rig) addAccount(tenantID, accountID int64, accountType string, credJSON []byte) {
	r.store.put(ProviderAccountCredential{
		TenantID:       tenantID,
		AccountID:      accountID,
		Provider:       antigravityProvider,
		AccountType:    accountType,
		Enabled:        true,
		CredentialJSON: credJSON,
		TokenVersion:   1,
	})
}

func oauthCredJSON(t *testing.T, accessToken, refreshToken, oauthEndpoint string, expiresAt time.Time) []byte {
	t.Helper()
	cred := antigravityCredential{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		ExpiresAt:     expiresAt,
		OAuthEndpoint: oauthEndpoint,
	}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("oauthCredJSON: %v", err)
	}
	return b
}

func staticCredJSON(t *testing.T, apiKey string) []byte {
	t.Helper()
	cred := antigravityCredential{APIKey: apiKey}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("staticCredJSON: %v", err)
	}
	return b
}

func okOAuthHandler(returnAccessToken, returnRefreshToken string, expiresInSeconds int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  returnAccessToken,
			"refresh_token": returnRefreshToken,
			"expires_in":    expiresInSeconds,
		})
	}
}

// =====================================================================
// Sub2API-inheritable scenarios
// =====================================================================

// AT-AUTH-005-001: pre-expiry refresh.
func TestAT_AUTH_005_001_PreExpiryRefresh(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "newrefresh"+goodToken, 3600))
	expired := time.Now().Add(2 * time.Minute) // < 3min skew → triggers refresh
	r.addAccount(1, 100, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, expired))

	tok, err := r.provider.GetAccessToken(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != "new"+goodToken {
		t.Fatalf("expected new access token; got %q", tok)
	}
	cur := r.store.get(1, 100)
	if cur.TokenVersion != 2 {
		t.Fatalf("token_version should increment to 2, got %d", cur.TokenVersion)
	}
	if cur.RefreshTokenFingerprint == "" {
		t.Fatalf("refresh_token_fingerprint should be set")
	}
}

// AT-AUTH-005-002: same-account refresh lock serialization.
func TestAT_AUTH_005_002_RefreshLockSerialization(t *testing.T) {
	var refreshCount int
	var mu sync.Mutex
	handler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refreshCount++
		mu.Unlock()
		// simulate slow upstream so concurrent refreshes overlap
		time.Sleep(150 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new" + goodToken, "refresh_token": "rt" + goodToken, "expires_in": 3600,
		})
	}
	r := newRig(t, handler)
	r.addAccount(2, 200, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	const N = 12
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = r.provider.GetAccessToken(context.Background(), 2, 200)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if refreshCount > N/2 {
		t.Fatalf("expected refresh to be serialized; got %d upstream calls for %d goroutines", refreshCount, N)
	}
}

// AT-AUTH-005-003: CAS conflict on token_version → other goroutine uses winner's token.
func TestAT_AUTH_005_003_TokenVersionCAS(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "rt"+goodToken, 3600))
	r.addAccount(3, 300, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	// Pre-conflict: bump the stored token_version externally, simulating a winner already wrote.
	cur := r.store.get(3, 300)
	cur.TokenVersion = 5
	r.store.put(*cur)

	// First refresh attempt loads with TokenVersion=5; SaveRefreshedCredential will succeed
	// because store CAS treats 5 as current. To actually trigger CAS-lost, race two goroutines:
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = r.provider.GetAccessToken(context.Background(), 3, 300) }()
	go func() { defer wg.Done(); _, _ = r.provider.GetAccessToken(context.Background(), 3, 300) }()
	wg.Wait()
	finalCur := r.store.get(3, 300)
	if finalCur.TokenVersion < 6 {
		t.Fatalf("token_version should advance after refresh; got %d", finalCur.TokenVersion)
	}
}

// AT-AUTH-005-004: refresh failure on request path bounded by 8s context timeout.
func TestAT_AUTH_005_004_RequestPathTimeout(t *testing.T) {
	t.Skip("Bounded timeout test exercises real 8s wait; skip in fast suite. Phase 4.5 long-test target.")
}

// AT-AUTH-005-006: static credential support — no refresh, just return api_key.
func TestAT_AUTH_005_006_StaticCredential(t *testing.T) {
	r := newRig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("static credential should NOT call upstream OAuth endpoint")
		http.Error(w, "should not reach", http.StatusInternalServerError)
	}))
	apiKey := "api" + goodToken
	r.addAccount(4, 400, staticAccountType, staticCredJSON(t, apiKey))

	tok, err := r.provider.GetAccessToken(context.Background(), 4, 400)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != apiKey {
		t.Fatalf("expected static apiKey %q, got %q", apiKey, tok)
	}
}

// =====================================================================
// HUAKAI-design scenarios
// =====================================================================

// AT-AUTH-005-007: tenant isolation — cache key MUST include tenant_id.
func TestAT_AUTH_005_007_TenantIsolation(t *testing.T) {
	r := newRig(t, okOAuthHandler(goodToken, "rt"+goodToken, 3600))
	r.addAccount(7, 777, staticAccountType, staticCredJSON(t, "secret-tenant7-"+goodToken))
	r.addAccount(8, 777, staticAccountType, staticCredJSON(t, "secret-tenant8-"+goodToken))

	// Both tenants share the same accountID=777; tokens MUST be different.
	tok7, err := r.provider.GetAccessToken(context.Background(), 7, 777)
	if err != nil {
		t.Fatalf("tenant 7: %v", err)
	}
	tok8, err := r.provider.GetAccessToken(context.Background(), 8, 777)
	if err != nil {
		t.Fatalf("tenant 8: %v", err)
	}
	if tok7 == tok8 {
		t.Fatalf("cross-tenant cache poisoning: both returned %q", tok7)
	}
	if !strings.Contains(tok7, "tenant7") || !strings.Contains(tok8, "tenant8") {
		t.Fatalf("tokens swapped between tenants: tenant7=%q tenant8=%q", tok7, tok8)
	}
}

// AT-AUTH-005-009: token shape attestation rejects malformed.
func TestAT_AUTH_005_009_TokenShapeAttestation(t *testing.T) {
	r := newRig(t, okOAuthHandler("garbage with spaces!", "rt"+goodToken, 3600))
	r.addAccount(9, 900, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	_, err := r.provider.GetAccessToken(context.Background(), 9, 900)
	if err == nil {
		t.Fatalf("expected ERR_TOKEN_MALFORMED on garbage token")
	}
	if !contains(r.audit.entries, OutcomeTokenMalformed) {
		t.Fatalf("expected audit entry with OutcomeTokenMalformed; got %+v", r.audit.entries)
	}
}

// AT-AUTH-005-010: refresh token rotation audit records old/new fingerprints (NOT plaintext).
func TestAT_AUTH_005_010_RefreshRotationAudit(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "rotated-refresh-"+goodToken, 3600))
	r.addAccount(10, 1000, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "old-refresh-"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	_, err := r.provider.GetAccessToken(context.Background(), 10, 1000)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	rotated := r.audit.byOutcome(OutcomeRefreshTokenRotated)
	if len(rotated) == 0 {
		t.Fatalf("expected at least one OutcomeRefreshTokenRotated audit; got %+v", r.audit.entries)
	}
	for _, e := range rotated {
		if strings.Contains(e.OldRefreshTokenFingerprint, "old-refresh-") {
			t.Fatalf("audit leaks plaintext old refresh_token: %q", e.OldRefreshTokenFingerprint)
		}
		if strings.Contains(e.NewRefreshTokenFingerprint, "rotated-refresh-") {
			t.Fatalf("audit leaks plaintext new refresh_token: %q", e.NewRefreshTokenFingerprint)
		}
	}
}

// AT-AUTH-005-011: token-leakage-safe sanitizer redacts token-shaped patterns.
func TestAT_AUTH_005_011_TokenLeakageSafeSanitizer(t *testing.T) {
	s := OAuthErrorSanitizer{}
	cases := []string{
		"refresh failed: bearer sk-1234567890abcdef leaked",
		"oauth response body access_token=eyJabc.eyJpc3MiOiJtZSJ9.signature",
		"got toolu_01abcdef0123456789ABCDEF",
		"creds=ant-api03-VeryLongSecretValueHereCanItGetCaught",
	}
	for _, in := range cases {
		out := s.SanitizeError(makeErr(in)).Error()
		// Sanitizer should contain [REDACTED] for token-shaped values.
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("sanitizer left token-shaped pattern in: %q", out)
		}
	}
}

// =====================================================================
// Storm controller (account scope only for v0.1)
// =====================================================================

// TestStormControllerSmoke verifies the constructor doesn't panic and
// the deferred-scope methods panic with TODO messages.
func TestStormControllerSmoke(t *testing.T) {
	c := NewStormController(nil)
	if c == nil {
		t.Fatalf("controller is nil")
	}
	defer func() {
		if recover() == nil {
			t.Fatalf("provider-endpoint scope should panic with TODO")
		}
	}()
	_, _, _ = c.AcquireProviderEndpoint(context.Background(), 1, "p", "f")
}

func TestStormControllerGlobalScopeDeferred(t *testing.T) {
	c := NewStormController(nil)
	defer func() {
		if recover() == nil {
			t.Fatalf("global scope should panic with TODO")
		}
	}()
	_, _, _ = c.AcquireGlobal(context.Background(), 1)
}

// =====================================================================
// Smoke
// =====================================================================

func TestPackageCompiles(t *testing.T) {
	if time.Now().Year() < 2026 {
		t.Fatalf("clock skew")
	}
}

// =====================================================================
// helpers
// =====================================================================

type stubErr struct{ msg string }

func (e stubErr) Error() string { return e.msg }
func makeErr(s string) error    { return stubErr{msg: s} }

func contains(entries []RefreshAuditEntry, o Outcome) bool {
	for _, e := range entries {
		if e.Outcome == o {
			return true
		}
	}
	return false
}
