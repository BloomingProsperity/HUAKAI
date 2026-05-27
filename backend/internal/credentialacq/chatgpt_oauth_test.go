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

// 缺陷：ChatGPT public CLI OAuth 是 PKCE-only；若接受 client_secret 注入，会把 public profile 变成任意 confidential app。
// 判别 mutation：允许 cfg.ClientSecret 非空时，本测试必须变红。
func TestChatGPTBuiltinProfileRejectsClientSecretInjection(t *testing.T) {
	cfg := chatgptBuiltinProfileConfig(OAuthClientConfig{ClientSecret: "from-request"})

	err := validateChatGPTBuiltinProfile(cfg)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled", err)
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Fatalf("err=%v want client_secret mismatch", err)
	}
}

// 缺陷：operator 若能覆盖内置 ClientID，会绕开 Owner 批准的 OpenAI public CLI profile。
// 判别 mutation：让 chatgptBuiltinProfileConfig 接受 override.ClientID 时，本测试必须变红。
func TestChatGPTBuiltinProfileRejectsOverriddenClientID(t *testing.T) {
	cfg := chatgptBuiltinProfileConfig(OAuthClientConfig{ClientID: "attacker-client"})
	if cfg.ClientID != chatgptOAuthClientID {
		t.Fatalf("cfg.ClientID=%q want built-in %q", cfg.ClientID, chatgptOAuthClientID)
	}
	if err := validateChatGPTBuiltinProfile(cfg); err != nil {
		t.Fatalf("built-in config rejected: %v", err)
	}

	cfg.ClientID = "attacker-client"
	err := validateChatGPTBuiltinProfile(cfg)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for client_id mismatch", err)
	}
}

// 缺陷：OpenAI 特定 authorize 参数若被通用 builder 吞掉，真实 ChatGPT OAuth 可能不返回预期 code / refresh 行为。
// 判别 mutation：删除 prompt/id_token_add_organizations/codex_cli_simplified_flow/offline_access 任一项时，本测试必须变红。
func TestChatGPTAuthorizeURLContainsOpenAISpecificParams(t *testing.T) {
	store, _ := newChatGPTOAuthTestStore(t, time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC))
	exchanger := newChatGPTOAuthExchanger()

	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(901), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	parsed, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v url=%q", err, start.AuthorizeURL)
	}
	if parsed.Scheme != "https" || parsed.Host != "auth.openai.com" || parsed.Path != "/oauth/authorize" {
		t.Fatalf("authorize endpoint=%s want OpenAI auth endpoint", parsed.String())
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"prompt":                     "login",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"client_id":                  chatgptOAuthClientID,
		"redirect_uri":               chatgptOAuthLoopbackRedirect,
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("query[%s]=%q want %q; authorize_url=%s", key, got, want, start.AuthorizeURL)
		}
	}
	if scope := q.Get("scope"); !strings.Contains(" "+scope+" ", " offline_access ") {
		t.Fatalf("scope=%q missing offline_access; authorize_url=%s", scope, start.AuthorizeURL)
	}
}

// 缺陷：redirect_uri 若只看 URL 语法，会接受攻击者 HTTPS host 或 127.0.0.1 loopback 变体。
// 判别 mutation：放宽 http/https 分支或默认接受任意 admin host 时，表格中的拒绝用例至少一条必须变红。
func TestChatGPTRedirectURIDualModeAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		allowlist []string
		ok        bool
	}{
		{name: "reject_attacker_https_callback", raw: "https://attacker.test/cb"},
		{name: "accept_localhost_loopback", raw: "http://localhost:1455/auth/callback", ok: true},
		{name: "reject_127_loopback", raw: "http://127.0.0.1:1455/auth/callback"},
		{name: "reject_admin_callback_without_allowlist", raw: "https://huakai.example/admin/v1/credentials/oauth-callback"},
		{name: "accept_admin_callback_with_allowlist", raw: "https://huakai.example/admin/v1/credentials/oauth-callback", allowlist: []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}, ok: true},
		{name: "reject_non_allowlisted_admin_callback", raw: "https://attacker.test/admin/v1/credentials/oauth-callback", allowlist: []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChatGPTRedirectURIWithHTTPSAdminAllowlist(tc.raw, tc.allowlist)
			if tc.ok && err != nil {
				t.Fatalf("validateChatGPTRedirectURI(%q)=%v want nil", tc.raw, err)
			}
			if !tc.ok && !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("validateChatGPTRedirectURI(%q)=%v want ErrFeatureDisabled", tc.raw, err)
			}
		})
	}
}

// 缺陷：admin HTTPS callback 若只校验 path，会让攻击者公网 host 接收 OAuth code。
// 判别 mutation：删除 allowlist 强制或只比较 path 时，本测试必须变红。
func TestChatGPTRedirectURIRejectsArbitraryHTTPSHostWithAdminPath(t *testing.T) {
	err := validateChatGPTRedirectURI("https://attacker.test/admin/v1/credentials/oauth-callback")
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for arbitrary HTTPS admin host", err)
	}
}

// 缺陷：admin HTTPS callback 必须来自 operator 静态 allowlist，不能来自启动请求体。
// 判别 mutation：忽略 allowlist 或只比较 path 时，匹配/不匹配断言至少一个必须变红。
func TestChatGPTRedirectURIAcceptsHTTPSAdminCallbackFromAllowlist(t *testing.T) {
	allowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	if err := validateChatGPTRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback", allowlist); err != nil {
		t.Fatalf("allowlisted admin callback rejected: %v", err)
	}
	if err := validateChatGPTRedirectURIWithHTTPSAdminAllowlist("https://attacker.test/admin/v1/credentials/oauth-callback", allowlist); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for non-allowlisted admin callback", err)
	}
}

// 缺陷：admin HTTPS callback 的 provider redirect 只会带 state/code；如果
// authorize request 的 redirect_uri 没保留 flow_id，mounted helper handler 会
// d.Sessions.Get("") 并让 admin OAuth flow 无法完成。
// 判别 mutation：删除 admin redirect_uri 的 flow_id query 注入时，本测试必须变红。
func TestChatGPTAuthorizeURLPreservesFlowIDInAdminMode(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 25, 0, 0, time.UTC)
	store, _ := newChatGPTOAuthTestStore(t, now)
	adminCallback := "https://huakai.example/admin/v1/credentials/oauth-callback"
	exchanger := NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(nil, []string{adminCallback}).(chatgptOAuthExchanger)

	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(906), OAuthClientConfig{
		RedirectURI: adminCallback,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	parsed, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v url=%q", err, start.AuthorizeURL)
	}
	redirectRaw := parsed.Query().Get("redirect_uri")
	redirectURL, err := url.Parse(redirectRaw)
	if err != nil {
		t.Fatalf("redirect_uri parse: %v raw=%q", err, redirectRaw)
	}
	if got := redirectURL.Query().Get("flow_id"); got != start.Session.ID {
		t.Fatalf("redirect_uri flow_id=%q want session id %q; redirect_uri=%s", got, start.Session.ID, redirectRaw)
	}
	stored, err := decryptStoredPKCEPayload(context.Background(), store, start.Session)
	if err != nil {
		t.Fatalf("decrypt stored PKCE payload: %v", err)
	}
	if stored.RedirectURI != redirectRaw {
		t.Fatalf("stored redirect_uri=%q want authorize redirect_uri %q", stored.RedirectURI, redirectRaw)
	}
}

// 缺陷：无 store 的 callback 若回退 fake exchanger，会让 OAuth-only 模式直接接收 JSON token。
// 判别 mutation：把 ExchangeOAuthCode 改成 NewPKCEFakeExchanger fallthrough 时，本测试必须变红。
func TestChatGPTOAuthExchangerExchangeOAuthCodeRequiresStore(t *testing.T) {
	exchanger := newChatGPTOAuthExchanger()

	candidate, err := exchanger.ExchangeOAuthCode(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 101, Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
	}, `{"access_token":"FAKE","refresh_token":"FAKE-RT"}`)
	if !errors.Is(err, ErrOAuthExchangerMissing) {
		t.Fatalf("err=%v want ErrOAuthExchangerMissing", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty", candidate.Payload)
	}
}

// 缺陷：ChatGPT callback 若没发 form-urlencoded authorization_code + PKCE verifier，真实 OAuth callback 会失效或泄漏 secret。
// 判别 mutation：写错 token URL、改成 JSON body、漏 verifier 或发 client_secret 时，本测试必须变红。
func TestChatGPTOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 5, 0, 0, time.UTC)
	store, _ := newChatGPTOAuthTestStore(t, now)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != chatgptOAuthTokenURL {
			t.Fatalf("token URL=%s want %s", r.URL.String(), chatgptOAuthTokenURL)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		if gotForm.Get("client_secret") != "" {
			t.Fatalf("client_secret must not be sent for PKCE-only ChatGPT OAuth: %v", gotForm)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"cg-access","refresh_token":"cg-refresh","id_token":"cg-id","scope":"openid email profile offline_access","expires_in":1800,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewChatGPTOAuthExchangerWithClient(client).(chatgptOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(902), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	_, _, err = CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "chatgpt-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "chatgpt-auth-code",
		"redirect_uri":  chatgptOAuthLoopbackRedirect,
		"client_id":     chatgptOAuthClientID,
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
	}
}

// 缺陷：OpenAI 未返回 refresh_token 时若静默保存 access-only，ChatGPT 账号续期会在短 TTL 后失败。
// 判别 mutation：删除 refresh_token 非空校验时，本测试必须变红。
func TestChatGPTOAuthCallbackRequiresRefreshTokenInResponse(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 10, 0, 0, time.UTC)
	store, _ := newChatGPTOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-without-refresh","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewChatGPTOAuthExchangerWithClient(client).(chatgptOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(903), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, err := exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, start.Session, start.State, "code")
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("err=%v want ErrInvalidTokenShape for access-only ChatGPT token response", err)
	}
	if err == nil || !strings.Contains(err.Error(), "missing refresh_token") {
		t.Fatalf("err=%v want explicit missing refresh_token message", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty after invalid token shape", candidate.Payload)
	}
}

// 缺陷：ChatGPT metadata 若只放 RedactedContext 或完全丢弃，会丢失后续账号识别；若把 PII 放 RedactedContext，会污染审计面。
// 判别 mutation：删除 cred metadata 写入或把 user/account id 写入 RedactedContext 时，本测试必须变红。
func TestChatGPTOAuthCallbackPersistsChatGPTMetadataInCredAndRedactedContext(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 15, 0, 0, time.UTC)
	store, _ := newChatGPTOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"AT-chatgpt",
				"refresh_token":"RT-chatgpt",
				"expires_in":3600,
				"token_type":"Bearer",
				"chatgpt_user_id":"user-123",
				"chatgpt_plan_type":"Plus",
				"chatgpt_account_id":"acct-456"
			}`)),
		}, nil
	})}
	exchanger := NewChatGPTOAuthExchangerWithClient(client).(chatgptOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(904), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, _, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	for key, want := range map[string]string{
		"chatgpt_user_id":        "user-123",
		"chatgpt_plan_type":      "Plus",
		"chatgpt_account_id":     "acct-456",
		"client_identity_source": chatgptApprovedProfileSource,
		"client_id_source":       chatgptApprovedProfileSource,
		"oauth_token_endpoint":   chatgptOAuthTokenURL,
		"client_id":              chatgptOAuthClientID,
		"refresh_token":          "RT-chatgpt",
		"session_token":          "AT-chatgpt",
	} {
		if got := stringFieldFromAny(payload[key]); got != want {
			t.Fatalf("payload[%s]=%q want %q; payload=%v", key, got, want, payload)
		}
	}
	if got := stringFieldFromAny(candidate.RedactedContext["client_identity_source"]); got != chatgptApprovedProfileSource {
		t.Fatalf("redacted context source=%q want %q", got, chatgptApprovedProfileSource)
	}
	if got := stringFieldFromAny(candidate.RedactedContext["chatgpt_plan_type_class"]); got != "Plus" {
		t.Fatalf("redacted plan class=%q want Plus", got)
	}
	for _, piiKey := range []string{"chatgpt_user_id", "chatgpt_account_id"} {
		if _, ok := candidate.RedactedContext[piiKey]; ok {
			t.Fatalf("RedactedContext leaked %s: %v", piiKey, candidate.RedactedContext)
		}
	}
}

// 缺陷：wiring 自检 helper 若总返回 true，会掩盖生产未注入受控 HTTP client 的启动错误。
// 判别 mutation：让 helper 对 zero-value exchanger 也返回 true 时，本测试必须变红。
func TestIsChatGPTOAuthExchangerWithExplicitClientDistinguishesInjectedClient(t *testing.T) {
	if IsChatGPTOAuthExchangerWithExplicitClient(newChatGPTOAuthExchanger()) {
		t.Fatal("zero-value exchanger reported explicit client")
	}
	if !IsChatGPTOAuthExchangerWithExplicitClient(NewChatGPTOAuthExchangerWithClient(&http.Client{})) {
		t.Fatal("injected client was not detected")
	}
	if IsChatGPTOAuthExchangerWithExplicitClient(NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)) {
		t.Fatal("fake exchanger reported explicit ChatGPT client")
	}
}

// 缺陷：admin callback path 若偏离实际 mount route，OpenAI redirect 会打到未挂载路径。
// 判别 mutation：把 chatgptAdminCallbackPath 改成旧路径时，本测试必须变红。
func TestChatGPTAdminCallbackPathMatchesMountedRoute(t *testing.T) {
	const mountedAdminCallbackPath = "/admin/v1/credentials/oauth-callback"
	if chatgptAdminCallbackPath != mountedAdminCallbackPath {
		t.Fatalf("chatgptAdminCallbackPath=%q want mounted route %q", chatgptAdminCallbackPath, mountedAdminCallbackPath)
	}
}

func newChatGPTOAuthTestStore(t *testing.T, now time.Time) (*PostgresSessionStore, *testSessionDB) {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("h", 32)))
	if err != nil {
		t.Fatal(err)
	}
	db := newTestSessionDB(now)
	return NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now }), db
}

func chatgptStartInput(accountID int64) StartInput {
	return StartInput{
		TenantID: 1, ProviderAccountID: accountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}
}
