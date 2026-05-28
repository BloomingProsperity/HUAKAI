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
	"github.com/google/uuid"
)

// 缺陷：Gemini public CLI profile 若不强制 operator 填 client_secret，会退回不符合 D-1=A 的 PKCE-only。
// 判别 mutation：删除 client_secret 校验时，本测试必须变红。
func TestGeminiBuiltinProfileRejectsMissingClientSecret(t *testing.T) {
	cfg := geminiBuiltinProfileConfig(OAuthClientConfig{})

	err := validateGeminiBuiltinProfile(cfg)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled", err)
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Fatalf("err=%v want client_secret mismatch", err)
	}
}

// 缺陷：operator 若能覆盖内置 ClientID，会把 public CLI allowlist 变成任意 OAuth app。
// 判别 mutation：让 geminiBuiltinProfileConfig 接受 override.ClientID 时，本测试必须变红。
func TestGeminiBuiltinProfileRejectsOverriddenClientID(t *testing.T) {
	cfg := geminiBuiltinProfileConfig(OAuthClientConfig{
		ClientID:     "attacker-client.apps.googleusercontent.com",
		ClientSecret: "operator-secret",
	})
	if cfg.ClientID != geminiPublicCLIClientID {
		t.Fatalf("cfg.ClientID=%q want built-in %q", cfg.ClientID, geminiPublicCLIClientID)
	}
	if err := validateGeminiBuiltinProfile(cfg); err != nil {
		t.Fatalf("built-in config rejected: %v", err)
	}

	cfg.ClientID = "attacker-client.apps.googleusercontent.com"
	err := validateGeminiBuiltinProfile(cfg)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for client_id mismatch", err)
	}
}

// 缺陷：Google OAuth authorize URL 若不显式请求 offline + consent，不会稳定返回 refresh_token。
// 判别 mutation：删除 access_type=offline 或 prompt=consent 任何一个设置时，本测试必须变红。
func TestGeminiStartOAuthFlowRequestsOfflineAccessAndConsent(t *testing.T) {
	store, _ := newGeminiOAuthTestStore(t, time.Date(2026, 5, 27, 8, 20, 0, 0, time.UTC))
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeCodeAssist, nil, "operator-secret").(geminiPublicCLIOAuthExchanger)

	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 704), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	parsed, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v url=%q", err, start.AuthorizeURL)
	}
	q := parsed.Query()
	if got := q.Get("access_type"); got != "offline" {
		t.Fatalf("access_type=%q want offline; authorize_url=%s", got, start.AuthorizeURL)
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Fatalf("prompt=%q want consent; authorize_url=%s", got, start.AuthorizeURL)
	}
}

// 缺陷：Gemini public CLI client_secret 若来自 StartOAuthFlow caller config，
// admin request body 可绕过 operator env secret。判别 mutation：把 cfg.ClientSecret
// 写回 stored PKCE payload 时，本测试必须变红。
func TestGeminiPublicCLIOAuthExchangerStoresInjectedClientSecret(t *testing.T) {
	store, _ := newGeminiOAuthTestStore(t, time.Date(2026, 5, 27, 10, 10, 0, 0, time.UTC))
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(
		credentialstore.AuthModeCodeAssist,
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("token endpoint must not be called during OAuth start")
			return nil, errors.New("unreachable")
		})},
		"from-env",
	).(geminiPublicCLIOAuthExchanger)

	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 706), OAuthClientConfig{
		ClientSecret: "from-request",
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	stored, err := decryptStoredPKCEPayload(context.Background(), store, start.Session)
	if err != nil {
		t.Fatalf("decrypt stored PKCE payload: %v", err)
	}
	if stored.ClientSecret != "from-env" {
		t.Fatalf("stored ClientSecret=%q want env-injected secret", stored.ClientSecret)
	}
	if stored.ClientSecret == "from-request" {
		t.Fatal("request client_secret leaked into stored PKCE payload")
	}
}

// 缺陷：redirect_uri 若只做 URL 语法检查，会接受攻击者 HTTPS callback 或 127.0.0.1 loopback 变体。
// 判别 mutation：放宽 http/https 分支或默认接受任意 admin host 时，表格中的拒绝用例至少一条必须变红。
func TestGeminiRedirectURIDualModeAllowlist(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "reject_attacker_https_callback", raw: "https://attacker.test/cb"},
		{name: "accept_localhost_loopback", raw: "http://localhost:8085/oauth2callback", ok: true},
		{name: "reject_127_loopback", raw: "http://127.0.0.1:8085/oauth2callback"},
		{name: "reject_admin_callback_without_allowlist", raw: "https://huakai.example/admin/v1/credentials/oauth-callback"},
		{name: "reject_private_https_ip", raw: "https://192.168.1.1/cb"},
		{name: "reject_loopback_without_port", raw: "http://localhost/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGeminiRedirectURI(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("validateGeminiRedirectURI(%q)=%v want nil", tc.raw, err)
			}
			if !tc.ok && !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("validateGeminiRedirectURI(%q)=%v want ErrFeatureDisabled", tc.raw, err)
			}
		})
	}
}

// 缺陷：admin callback allowlist path 若偏离实际 mount route，Google redirect 会打到未挂载路径。
// 判别 mutation：把 geminiAdminCallbackPath 改回旧的 /admin/oauth/gemini/callback 时，本测试必须变红。
func TestGeminiAdminCallbackPathMatchesMountedRoute(t *testing.T) {
	const mountedAdminCallbackPath = "/admin/v1/credentials/oauth-callback"
	if geminiAdminCallbackPath != mountedAdminCallbackPath {
		t.Fatalf("geminiAdminCallbackPath=%q want mounted route %q", geminiAdminCallbackPath, mountedAdminCallbackPath)
	}
}

// 缺陷：admin HTTPS callback 若只校验 path，会让攻击者公网 host 接收 OAuth code。
// 判别 mutation：删除 allowlist 强制或只比较 path 时，本测试必须变红。
func TestGeminiRedirectURIRejectsArbitraryHTTPSHostWithAdminPath(t *testing.T) {
	err := validateGeminiRedirectURI("https://attacker.test/admin/v1/credentials/oauth-callback")
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for arbitrary HTTPS admin host", err)
	}
}

// 缺陷：admin HTTPS callback 必须来自 operator 静态 allowlist，不能来自启动请求体。
// 判别 mutation：忽略 allowlist 或只比较 path 时，匹配/不匹配断言至少一个必须变红。
func TestGeminiRedirectURIAcceptsHTTPSAdminCallbackFromAllowlist(t *testing.T) {
	allowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback", allowlist); err != nil {
		t.Fatalf("allowlisted admin callback rejected: %v", err)
	}
	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://attacker.test/admin/v1/credentials/oauth-callback", allowlist); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for non-allowlisted admin callback", err)
	}
}

// 缺陷：admin HTTPS callback allowlist 若静默剥离 fragment，攻击者可把
// #evil 形式的 redirect_uri 伪装成裸 allowlist 路径通过校验。
// 判别 mutation：geminiAdminCallbackAllowlistKey 退回 parsed.Fragment = "" 时，本测试必须变红。
func TestGeminiRedirectURIRejectsFragmentEvenWhenPathAllowlisted(t *testing.T) {
	allowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback", allowlist); err != nil {
		t.Fatalf("bare allowlisted admin callback rejected: %v", err)
	}
	err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback#evil", allowlist)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for fragment-bearing admin callback", err)
	}
}

// 缺陷：url.Query 会吞掉畸形 query pair，导致 evil=%zz 被规范化成裸 allowlist key 通过。
// 判别 mutation：geminiAdminCallbackAllowlistKey 退回 parsed.Query() 时，两个畸形 query 断言必须变红。
func TestGeminiRedirectURIRejectsMalformedQueryEvenWhenPathAllowlisted(t *testing.T) {
	const flowID = "11111111-1111-4111-8111-111111111111"
	allowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}

	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback", allowlist); err != nil {
		t.Fatalf("bare allowlisted admin callback rejected: %v", err)
	}
	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist("https://huakai.example/admin/v1/credentials/oauth-callback?flow_id="+flowID, allowlist); err != nil {
		t.Fatalf("single flow_id admin callback rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "malformed_extra_query",
			raw:  "https://huakai.example/admin/v1/credentials/oauth-callback?evil=%zz",
		},
		{
			name: "flow_id_plus_malformed_extra_query",
			raw:  "https://huakai.example/admin/v1/credentials/oauth-callback?flow_id=" + flowID + "&evil=%zz",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGeminiRedirectURIWithHTTPSAdminAllowlist(tc.raw, allowlist)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("err=%v want ErrFeatureDisabled for malformed admin callback query %q", err, tc.raw)
			}
		})
	}
}

// 缺陷：admin HTTPS callback allowlist key 若容忍 URL userinfo，https://attacker@host 会伪装成同 host/path。
// 判别 mutation：删除 parsed.User != nil 检查时，本测试必须变红。
func TestGeminiRedirectURIRejectsUserInfoEvenWhenPathAllowlisted(t *testing.T) {
	allowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	const raw = "https://attacker@huakai.example/admin/v1/credentials/oauth-callback"

	err := validateGeminiRedirectURIWithHTTPSAdminAllowlist(raw, allowlist)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for userinfo-bearing admin callback", err)
	}
	if key, ok := geminiAdminCallbackAllowlistKey(raw); ok {
		t.Fatalf("allowlist key accepted userinfo-bearing URL: key=%q", key)
	}
}

// 缺陷：admin HTTPS callback 的 provider redirect 只会带 state/code；如果
// authorize request 的 redirect_uri 没保留 flow_id，helper callback 不能查回 session。
// 判别 mutation：buildGeminiAuthorizeURL 不调用 geminiRedirectURIWithFlowID 时，本测试必须变红。
func TestGeminiHTTPSAdminRedirectPreservesFlowID(t *testing.T) {
	const flowID = "11111111-1111-4111-8111-111111111111"
	cfg := geminiBuiltinProfileConfig(OAuthClientConfig{
		ClientSecret: "operator-secret",
		RedirectURI:  "https://huakai.example/admin/v1/credentials/oauth-callback",
	})

	redirectURL := geminiAuthorizeRedirectURL(t, buildGeminiAuthorizeURL(cfg, "state", "challenge", flowID))
	if got := redirectURL.EscapedPath(); got != geminiAdminCallbackPath {
		t.Fatalf("redirect path=%q want %q", got, geminiAdminCallbackPath)
	}
	if got := redirectURL.Query().Get("flow_id"); got != flowID {
		t.Fatalf("redirect_uri flow_id=%q want %q; redirect_uri=%s", got, flowID, redirectURL.String())
	}
}

// 缺陷：loopback 本地模式不走 admin helper session 查询，若强行加 flow_id 会改变
// Google public CLI loopback redirect_uri，破坏本地模式兼容。
// 判别 mutation：geminiRedirectURIWithFlowID 对 http loopback 也加 flow_id 时，本测试必须变红。
func TestGeminiLoopbackRedirectOmitsFlowID(t *testing.T) {
	cfg := geminiBuiltinProfileConfig(OAuthClientConfig{
		ClientSecret: "operator-secret",
		RedirectURI:  geminiOAuthLoopbackRedirect,
	})

	redirectURL := geminiAuthorizeRedirectURL(t, buildGeminiAuthorizeURL(cfg, "state", "challenge", "22222222-2222-4222-8222-222222222222"))
	if got := redirectURL.String(); got != geminiOAuthLoopbackRedirect {
		t.Fatalf("loopback redirect_uri=%q want unchanged %q", got, geminiOAuthLoopbackRedirect)
	}
	if got := redirectURL.Query().Get("flow_id"); got != "" {
		t.Fatalf("loopback redirect_uri flow_id=%q want empty; redirect_uri=%s", got, redirectURL.String())
	}
}

// 缺陷：caller 不传 ID 时若等 store 生成 session ID，authorize redirect_uri 已经
// 缺 flow_id，远程 admin callback 只能拿到 state/code 而无法定位 flow。
// 判别 mutation：StartOAuthFlow 不预生成 flowID 或不写入 stored redirect_uri 时，本测试必须变红。
func TestGeminiStartOAuthFlowGeneratesFlowIDWhenEmpty(t *testing.T) {
	store, _ := newGeminiOAuthTestStore(t, time.Date(2026, 5, 28, 8, 0, 0, 0, time.UTC))
	adminCallback := "https://huakai.example/admin/v1/credentials/oauth-callback"
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(
		credentialstore.AuthModeCodeAssist,
		nil,
		"operator-secret",
		[]string{adminCallback},
	).(geminiPublicCLIOAuthExchanger)

	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 707), OAuthClientConfig{
		RedirectURI: adminCallback,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if _, err := uuid.Parse(start.Session.ID); err != nil {
		t.Fatalf("session id=%q want generated uuid: %v", start.Session.ID, err)
	}
	redirectRaw := geminiAuthorizeRedirectURI(t, start.AuthorizeURL)
	redirectURL, err := url.Parse(redirectRaw)
	if err != nil {
		t.Fatalf("redirect_uri parse: %v raw=%q", err, redirectRaw)
	}
	if got := redirectURL.Query().Get("flow_id"); got != start.Session.ID {
		t.Fatalf("redirect_uri flow_id=%q want generated session id %q; redirect_uri=%s", got, start.Session.ID, redirectRaw)
	}
	if start.Session.RedirectURI != redirectRaw {
		t.Fatalf("session redirect_uri=%q want authorize redirect_uri %q", start.Session.RedirectURI, redirectRaw)
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
func TestGeminiPublicCLIOAuthExchangerExchangeOAuthCodeRequiresStore(t *testing.T) {
	exchanger := newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeCodeAssist)

	candidate, err := exchanger.ExchangeOAuthCode(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 101, Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist,
	}, `{"access_token":"FAKE","refresh_token":"FAKE-RT"}`)
	if !errors.Is(err, ErrOAuthExchangerMissing) {
		t.Fatalf("err=%v want ErrOAuthExchangerMissing", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty", candidate.Payload)
	}
}

// 缺陷：Gemini token exchange 若没发到 Google token endpoint 或没用 form body，真实 OAuth callback 会失效。
// 判别 mutation：写错 token URL 或改成 JSON body 时，本测试必须变红。
func TestGeminiOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	store, _ := newGeminiOAuthTestStore(t, now)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != geminiOAuthTokenURL {
			t.Fatalf("token URL=%s want %s", r.URL.String(), geminiOAuthTokenURL)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"gem-access","refresh_token":"gem-refresh","scope":"profile email","expires_in":1800,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeCodeAssist, client, "operator-secret").(geminiPublicCLIOAuthExchanger)
	exchanger.now = func() time.Time { return now }

	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 701), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	_, _, err = CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "gemini-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "gemini-auth-code",
		"redirect_uri":  geminiOAuthLoopbackRedirect,
		"client_id":     geminiPublicCLIClientID,
		"client_secret": "operator-secret",
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
	}
}

// 缺陷：Google 即使请求 offline+consent 也可能不返回 refresh_token，若静默保存 access-only，账号续期会在 1h 后失败。
// 判别 mutation：删除 exchangeAuthorizationCodeForm 的 refresh_token 非空校验时，本测试必须变红。
func TestGeminiOAuthCallbackRequiresRefreshTokenInResponse(t *testing.T) {
	now := time.Date(2026, 5, 27, 8, 6, 0, 0, time.UTC)
	store, _ := newGeminiOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-without-refresh","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeCodeAssist, client, "operator-secret").(geminiPublicCLIOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 705), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, err := exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, start.Session, start.State, "code")
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("err=%v want ErrInvalidTokenShape for access-only Gemini token response", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty after invalid token shape", candidate.Payload)
	}
}

// 缺陷：callback 产物若不写 client_identity_source，后续审计无法区分内置 public CLI profile。
// 判别 mutation：删除 RedactedContext 或 payload 中的 client_identity_source 写入时，本测试必须变红。
func TestGeminiOAuthCallbackSetsClientIdentitySourceInRedactedContext(t *testing.T) {
	now := time.Date(2026, 5, 27, 8, 5, 0, 0, time.UTC)
	store, _ := newGeminiOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT","refresh_token":"RT","expires_in":3600}`)),
		}, nil
	})}
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeGoogleOne, client, "operator-secret").(geminiPublicCLIOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeGoogleOne, 702), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, _, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if got := stringFieldFromAny(candidate.RedactedContext["client_identity_source"]); got != geminiApprovedProfileSource {
		t.Fatalf("redacted context source=%q want %q", got, geminiApprovedProfileSource)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if got := stringFieldFromAny(payload["client_identity_source"]); got != geminiApprovedProfileSource {
		t.Fatalf("payload source=%q want %q; payload=%v", got, geminiApprovedProfileSource, payload)
	}
}

// 缺陷：wiring 自检 helper 若总返回 true，会掩盖生产未注入受控 HTTP client 的启动错误。
// 判别 mutation：让 helper 对 zero-value exchanger 也返回 true 时，本测试必须变红。
func TestIsGeminiPublicCLIOAuthExchangerWithExplicitClientDistinguishesInjectedClient(t *testing.T) {
	if IsGeminiPublicCLIOAuthExchangerWithExplicitClient(newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeCodeAssist)) {
		t.Fatal("zero-value exchanger reported explicit client")
	}
	if !IsGeminiPublicCLIOAuthExchangerWithExplicitClient(NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeCodeAssist, &http.Client{}, "operator-secret")) {
		t.Fatal("injected client was not detected")
	}
	if IsGeminiPublicCLIOAuthExchangerWithExplicitClient(NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)) {
		t.Fatal("fake exchanger reported explicit Gemini client")
	}
}

// 缺陷：解密后的 stored PKCE payload 若不二次校验，攻击者改写 token_url/client_id 后仍可能触发出站交换。
// 判别 mutation：删除 ExchangeOAuthCodeWithStore 中的 stored payload re-validate 时，本测试必须变红。
func TestExchangeOAuthCodeWithStoreRevalidatesBuiltinProfileAfterDecrypt(t *testing.T) {
	now := time.Date(2026, 5, 27, 8, 10, 0, 0, time.UTC)
	store, db := newGeminiOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("token endpoint must not be called after stored payload tamper")
		return nil, errors.New("unreachable")
	})}
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(credentialstore.AuthModeCodeAssist, client, "operator-secret").(geminiPublicCLIOAuthExchanger)
	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 703), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	tampered := storedPKCEPayload{
		CodeVerifier: start.CodeVerifier,
		TokenURL:     "https://attacker.example/token",
		ClientID:     "attacker-client.apps.googleusercontent.com",
		ClientSecret: "operator-secret",
		RedirectURI:  geminiOAuthLoopbackRedirect,
		Scopes:       strings.Fields(geminiOAuthScope),
	}
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal tampered payload: %v", err)
	}
	ciphertext, metadata, _, err := store.EncryptTransientPayload(context.Background(), raw, pkceAADFromSession(start.Session))
	if err != nil {
		t.Fatalf("EncryptTransientPayload: %v", err)
	}
	session := start.Session
	session.EncryptedPKCEVerifier = ciphertext
	session.NonceHash = metadata
	db.mu.Lock()
	row := db.rows[start.Session.ID]
	row.EncryptedPKCEVerifier = ciphertext
	row.NonceHash = metadata
	db.rows[start.Session.ID] = row
	db.mu.Unlock()

	_, err = exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, session, start.State, "code")
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for tampered stored profile", err)
	}
}

func newGeminiOAuthTestStore(t *testing.T, now time.Time) (*PostgresSessionStore, *testSessionDB) {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("g", 32)))
	if err != nil {
		t.Fatal(err)
	}
	db := newTestSessionDB(now)
	return NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now }), db
}

func geminiStartInput(authMode string, accountID int64) StartInput {
	return StartInput{
		TenantID: 1, ProviderAccountID: accountID,
		Vendor: credentialstore.VendorGemini, AuthMode: authMode,
		ActorID: "owner", ActorRole: "platform_admin",
	}
}

func geminiAuthorizeRedirectURI(t *testing.T, authorizeURL string) string {
	t.Helper()
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v url=%q", err, authorizeURL)
	}
	redirectRaw := parsed.Query().Get("redirect_uri")
	if redirectRaw == "" {
		t.Fatalf("authorize URL missing redirect_uri: %s", authorizeURL)
	}
	return redirectRaw
}

func geminiAuthorizeRedirectURL(t *testing.T, authorizeURL string) *url.URL {
	t.Helper()
	redirectRaw := geminiAuthorizeRedirectURI(t, authorizeURL)
	redirectURL, err := url.Parse(redirectRaw)
	if err != nil {
		t.Fatalf("redirect_uri parse: %v raw=%q", err, redirectRaw)
	}
	return redirectURL
}
