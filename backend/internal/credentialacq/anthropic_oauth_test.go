package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestClaudeAIOAuthExchangerUsesBuiltinProfile(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)
	exchanger := newClaudeAIOAuthExchanger()

	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	authorizeURL, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if authorizeURL.Host != "claude.ai" {
		t.Fatalf("authorize host=%q want claude.ai; url=%s", authorizeURL.Host, start.AuthorizeURL)
	}
	q := authorizeURL.Query()
	if claudeAIOAuthPublicClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Fatalf("built-in client ID constant=%q want Owner-approved value", claudeAIOAuthPublicClientID)
	}
	if got := q.Get("client_id"); got != claudeAIOAuthPublicClientID {
		t.Fatalf("client_id=%q want built-in Claude AI OAuth public client", got)
	}
	if scope := q.Get("scope"); !strings.Contains(scope, "user:inference") {
		t.Fatalf("scope=%q missing user:inference", scope)
	}
	if start.Session.ClientIdentitySource != ClientSourcePublicCLI {
		t.Fatalf("client_identity_source=%q want %q", start.Session.ClientIdentitySource, ClientSourcePublicCLI)
	}
}

func TestClaudeAIOAuthExchangerRejectsRuntimeEndpointOverride(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 5, 0, 0, time.UTC)
	store, db := newClaudeAIOAuthTestStore(t, now)
	exchanger := newClaudeAIOAuthExchanger()

	_, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 102,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{TokenURL: "http://attacker.test/token"})
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for runtime token endpoint override", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if got := len(db.rows); got != 0 {
		t.Fatalf("stored flows=%d want 0 after rejected built-in profile override", got)
	}
}

func TestClaudeAIOAuthExchangeUsesJSONBody(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 10, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)
	exchanger := claudeAIOAuthExchanger{now: func() time.Time { return now }}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	var gotBody map[string]string
	restore := withClaudeAIOAuthRoundTripper(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != claudeAIOAuthTokenURL {
			t.Fatalf("token URL=%s want %s", r.URL.String(), claudeAIOAuthTokenURL)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("content-type=%q want application/json", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if strings.Contains(string(raw), "application/x-www-form-urlencoded") || strings.Contains(string(raw), "code=") {
			t.Fatalf("body looks form encoded: %s", string(raw))
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("decode JSON body: %v body=%s", err, string(raw))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	}))
	defer restore()

	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 103,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{RedirectURI: "https://huakai.example.test/admin/oauth/anthropic/callback"})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "anthropic-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if session.Status != StatusValidated {
		t.Fatalf("status=%s want %s", session.Status, StatusValidated)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "anthropic-auth-code",
		"redirect_uri":  "https://huakai.example.test/admin/oauth/anthropic/callback",
		"client_id":     claudeAIOAuthPublicClientID,
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotBody[key]; got != want {
			t.Fatalf("body[%s]=%q want %q; full body=%v", key, got, want, gotBody)
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("candidate payload JSON: %v", err)
	}
	for key, want := range map[string]string{
		"access_token":           "AT",
		"refresh_token":          "RT",
		"client_identity_source": "approved_builtin_profile",
	} {
		if got := stringFieldFromAny(payload[key]); got != want {
			t.Fatalf("payload[%s]=%q want %q; payload=%v", key, got, want, payload)
		}
	}
	if got := stringFieldFromAny(payload["expires_at"]); got != now.Add(time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("expires_at=%q want %s", got, now.Add(time.Hour).UTC().Format(time.RFC3339))
	}
}

func TestClaudeAIOAuthExchangeRejectsInvalidGrant(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 20, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)
	exchanger := claudeAIOAuthExchanger{now: func() time.Time { return now }}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	restore := withClaudeAIOAuthRoundTripper(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"expired"}`)),
		}, nil
	}))
	defer restore()
	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 104,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	_, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "expired-code", registry)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
		t.Fatalf("err=%v want invalid_grant", err)
	}
	if session.Status != StatusFailed || session.ErrorClass != "exchange_failed" {
		t.Fatalf("status/class=%s/%s want failed/exchange_failed", session.Status, session.ErrorClass)
	}
}

// 走 admin handler 真实 dispatch 入口 CompleteOAuthCallbackWithRegistry +
// 项目默认 defaultExchangers — 验证 claude_ai_oauth 走真 exchanger 而不是被
// fake JSON code 旁路;cursor C1 教训 (helper-level 测不够,必须 default 入口)。
func TestAdminClaudeAIOAuthDefaultRegistryRejectsFakeJSONCode(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 30, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)

	exc, ok := defaultExchangers.Lookup(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth))
	if !ok {
		t.Fatal("default registry missing anthropic/claude_ai_oauth exchanger")
	}
	if _, isFake := exc.(pkceFakeExchanger); isFake {
		t.Fatal("default registry still wires fake exchanger; admin entry would accept attacker JSON code")
	}
	if _, ok := exc.(claudeAIOAuthExchanger); !ok {
		t.Fatalf("default registry exchanger type=%T want claudeAIOAuthExchanger", exc)
	}

	var tokenEndpointHits int
	restore := withClaudeAIOAuthRoundTripper(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenEndpointHits++
		if r.URL.String() != claudeAIOAuthTokenURL {
			t.Fatalf("token endpoint=%s want %s", r.URL.String(), claudeAIOAuthTokenURL)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode JSON body: %v body=%s", err, raw)
		}
		// 攻击者投递的整个 JSON 字符串必须作为 authorization code 透传到上游,
		// 而不是被本地 parseFakeTokenPayload 直接当 token 接受。
		if got["code"] != `{"access_token":"FAKE","refresh_token":"FAKE-RT"}` {
			t.Fatalf("forwarded code=%q want the raw attacker JSON string", got["code"])
		}
		if got["client_id"] != claudeAIOAuthPublicClientID {
			t.Fatalf("forwarded client_id=%q want builtin", got["client_id"])
		}
		// 上游用 invalid_grant 拒绝,任何 fake bypass 必失败。
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"code is not an authorization code"}`)),
		}, nil
	}))
	defer restore()

	start, err := newClaudeAIOAuthExchanger().StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 555,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, `{"access_token":"FAKE","refresh_token":"FAKE-RT"}`, defaultExchangers)
	if err == nil {
		t.Fatalf("fake JSON code accepted by admin entry; candidate=%+v session=%+v", candidate, session)
	}
	if tokenEndpointHits != 1 {
		t.Fatalf("token endpoint hits=%d want 1 (fake bypass would skip the upstream POST)", tokenEndpointHits)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
		t.Fatalf("err=%v want upstream invalid_grant signal", err)
	}
	if session.Status != StatusFailed || session.ErrorClass != "exchange_failed" {
		t.Fatalf("status/class=%s/%s want failed/exchange_failed", session.Status, session.ErrorClass)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload populated=%s want empty on failure", candidate.Payload)
	}
}

func newClaudeAIOAuthTestStore(t *testing.T, now time.Time) (*PostgresSessionStore, *testSessionDB) {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("c", 32)))
	if err != nil {
		t.Fatal(err)
	}
	db := newTestSessionDB(now)
	return NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now }), db
}

func withClaudeAIOAuthRoundTripper(t *testing.T, rt http.RoundTripper) func() {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = rt
	return func() { http.DefaultTransport = old }
}

func stringFieldFromAny(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(value.(string), `"`))
}
