package credentialworker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func TestAdapterRegistryRegistersRealAndMockOnlyVendors(t *testing.T) {
	registry := NewAdapterRegistry()
	real := map[string]RefreshAdapter{
		"anthropic": adapters.AnthropicRefresh{},
		"openai":    adapters.OpenAIRefresh{},
		"gemini":    adapters.GeminiRefresh{},
		"codex":     adapters.CodexRefresh{},
	}
	for name, adapter := range real {
		if err := registry.Register(name, adapter); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	for _, name := range MockOnlyProviders {
		if err := registry.Register(name, MockOnlyAdapter{}); err != nil {
			t.Fatalf("Register mock-only %s: %v", name, err)
		}
	}
	for name := range real {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("Lookup(%s) missing", name)
		}
	}
	for _, name := range MockOnlyProviders {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("Lookup mock-only %s missing", name)
		}
	}
	if got, want := len(registry.Names()), 4+len(MockOnlyProviders); got != want {
		t.Fatalf("registered names=%d, want %d", got, want)
	}
}

func TestAdapterRegistryDuplicateRegisterRejected(t *testing.T) {
	registry := NewAdapterRegistry()
	if err := registry.Register("openai", adapters.OpenAIRefresh{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := registry.Register(" OPENAI ", adapters.OpenAIRefresh{}); err == nil {
		t.Fatal("duplicate register must be rejected")
	}
}

func TestOpenAIRefreshHTTPRoundTripRetriesOnce(t *testing.T) {
	var attempts int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return httpStatusResponse(http.StatusBadGateway, "retry me"), nil
		}
		assertForm(t, r, map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "rt-old",
			"client_id":     "cid",
			"scope":         "openid",
		})
		return tokenJSONResponse("openai-new", "openai-rt"), nil
	})}

	newCredential, expiresAt, err := (adapters.OpenAIRefresh{Endpoint: "http://mock.local/openai", Scope: "openid", HTTPClient: client}).RefreshForProvider(context.Background(), 1, "openai", testCredential())
	assertRefreshResult(t, newCredential, expiresAt, err, "openai-new", "openai-rt")
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts=%d, want 2", got)
	}
}

func TestAnthropicRefreshHTTPRoundTrip(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("content-type=%q, want json", ct)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// ANT-3 D-4=B: client_id 来自 operator 配置 (r.ClientID) 或 HUAKAI
		// 硬编 anthropicoauth.AnthropicPublicCLIClientID,不再读 credential
		// payload — 测试 fixture 期望从 cid (credential payload) 改为
		// operator-injected "operator-cid",验证 SSRF guard 与正常 refresh 路径并存。
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-old" || body["client_id"] != "operator-cid" {
			t.Fatalf("bad anthropic body: %#v", body)
		}
		return tokenJSONResponse("anthropic-new", "anthropic-rt"), nil
	})}

	newCredential, expiresAt, err := (adapters.AnthropicRefresh{Endpoint: "http://mock.local/anthropic", ClientID: "operator-cid", HTTPClient: client}).RefreshForProvider(context.Background(), 2, "anthropic", testCredential())
	assertRefreshResult(t, newCredential, expiresAt, err, "anthropic-new", "anthropic-rt")
}

func TestGeminiRefreshHTTPRoundTrip(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertForm(t, r, map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "rt-old",
			"client_id":     "cid",
			"client_secret": "secret",
		})
		return tokenJSONResponse("gemini-new", "gemini-rt"), nil
	})}

	newCredential, expiresAt, err := (adapters.GeminiRefresh{
		Endpoint:            "http://mock.local/gemini",
		ClientID:            "cid",
		ClientSecret:        "secret",
		HTTPClient:          client,
		RequireClientSecret: true,
	}).RefreshForProvider(context.Background(), 3, "gemini", testCredential())
	assertRefreshResult(t, newCredential, expiresAt, err, "gemini-new", "gemini-rt")
}

func TestCodexRefreshReusesOpenAIHTTPRoundTrip(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertForm(t, r, map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "rt-old",
			"client_id":     "operator-cid",
			"scope":         "operator-scope",
		})
		return tokenJSONResponse("codex-new", "codex-rt"), nil
	})}

	adapter := adapters.NewCodexRefresh("http://mock.local/codex", "operator-cid", "operator-scope", client)
	newCredential, expiresAt, err := adapter.RefreshForProvider(context.Background(), 4, "codex", testCredential())
	assertRefreshResult(t, newCredential, expiresAt, err, "codex-new", "codex-rt")
}

func TestCodexRefreshRejectsCredentialSuppliedOAuthConfig(t *testing.T) {
	// 修掉的回归:生产的 credentialworker Codex adapter 在缺少 operator
	// endpoint/client/scope 时必须 fail closed。Mutation 自检:回退到 credential
	// oauth_token_endpoint 会调用 HTTP 客户端,使本测试转红。
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return tokenJSONResponse("unexpected", "unexpected-rt"), nil
	})}

	adapter := adapters.NewCodexRefresh("", "", "", client)
	_, _, err := adapter.RefreshForProvider(context.Background(), 4, "codex", []byte(`{
		"refresh_token":"rt-old",
		"client_id":"credential-cid",
		"scope":"credential-scope",
		"oauth_token_endpoint":"http://evil.attacker.test/token"
	}`))
	if !errors.Is(err, adapters.ErrCodexOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrCodexOAuthConfigRequired", err)
	}
	if called {
		t.Fatal("credential-supplied Codex token endpoint was used")
	}
}

func TestRegistryRefresherMockOnlyProviderReturnsErrMockOnly(t *testing.T) {
	registry := NewAdapterRegistry()
	if err := registry.Register("cursor", MockOnlyAdapter{}); err != nil {
		t.Fatalf("register cursor: %v", err)
	}
	store := &memoryRefreshStore{account: RefreshAccount{AccountID: 5, ProviderID: 50, ProviderName: "cursor", CurrentCredential: testCredential()}}
	err := NewRegistryRefresher(registry, store).RefreshForProvider(context.Background(), 50, 5)
	if !errors.Is(err, ErrMockOnly) {
		t.Fatalf("err=%v, want ErrMockOnly", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("mock-only path must not save credential: %s", string(store.saved))
	}
}

func TestRegistryRefresherMissingNonMockProviderReturnsMissing(t *testing.T) {
	store := &memoryRefreshStore{account: RefreshAccount{AccountID: 6, ProviderID: 60, ProviderName: "unknown-real", CurrentCredential: testCredential()}}
	err := NewRegistryRefresher(NewAdapterRegistry(), store).RefreshForProvider(context.Background(), 60, 6)
	if !errors.Is(err, ErrProviderAdapterMissing) {
		t.Fatalf("err=%v, want ErrProviderAdapterMissing", err)
	}
}

func testCredential() []byte {
	return []byte(`{"access_token":"old","refresh_token":"rt-old","client_id":"cid","client_secret":"secret","keep":"yes"}`)
}

func assertForm(t *testing.T, r *http.Request, want map[string]string) {
	t.Helper()
	if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
		t.Fatalf("content-type=%q, want form", ct)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	for key, value := range want {
		if got := values.Get(key); got != value {
			t.Fatalf("form %s=%q, want %q; all=%v", key, got, value, values)
		}
	}
}

func tokenJSONResponse(accessToken, refreshToken string) *http.Response {
	return jsonResponse(`{"access_token":"` + accessToken + `","refresh_token":"` + refreshToken + `","token_type":"bearer","expires_in":3600}`)
}

func httpStatusResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertRefreshResult(t *testing.T, raw []byte, expiresAt time.Time, err error, accessToken, refreshToken string) {
	t.Helper()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if time.Until(expiresAt) <= 30*time.Minute {
		t.Fatalf("expiresAt=%s, want future token expiry", expiresAt)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal new credential: %v", err)
	}
	if got["access_token"] != accessToken || got["refresh_token"] != refreshToken || got["keep"] != "yes" {
		t.Fatalf("new credential=%v", got)
	}
}

type memoryRefreshStore struct {
	account      RefreshAccount
	saved        []byte
	savedExpires time.Time
}

func (s *memoryRefreshStore) LoadRefreshAccount(context.Context, int64) (RefreshAccount, error) {
	return s.account, nil
}

func (s *memoryRefreshStore) SaveRefreshCredential(_ context.Context, account RefreshAccount, newCredential []byte, expiresAt time.Time) error {
	s.account = account
	s.saved = append([]byte(nil), newCredential...)
	s.savedExpires = expiresAt
	return nil
}
