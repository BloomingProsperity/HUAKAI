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

// 缺陷:codex 浏览器登录 authorize URL 若被通用 BuildAuthorizeURL 吞掉 codex 特定参数,
// 真实 OpenAI 端不会返回 organization-aware code / 简化流。
// 判别 mutation:删除 buildCodexAuthorizeURL 里 codex_cli_simplified_flow(或
// id_token_add_organizations / prompt / response_type)任一项时,本测试必须变红。
func TestCodexWebAuthorizeURLContainsCodexSpecificParams(t *testing.T) {
	store, _ := newCodexWebOAuthTestStore(t, time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC))
	exchanger := newCodexWebOAuthExchanger()

	start, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(901), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.AuthType != AuthTypePKCE {
		t.Fatalf("auth_type=%q want pkce", start.AuthType)
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
		"response_type":              "code",
		"prompt":                     "login",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"code_challenge_method":      "S256",
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

// 缺陷:codex web callback 若没发 form-urlencoded authorization_code + PKCE verifier,
// 或返回的凭据没同时落 access_token / session_token / refresh_token,会让 sessionFirst
// 的 codex runtime 取不到 session token 或续期短 TTL 后失败。
// 判别 mutation:写错 token URL、改成 JSON body、漏 verifier、发 client_secret,
// 或删除 tokenCandidatePayload 的 session_token 镜像时,本测试必须变红。
func TestCodexWebOAuthCallbackPostsAuthorizationCodeAndYieldsSessionAccessRefresh(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 5, 0, 0, time.UTC)
	store, _ := newCodexWebOAuthTestStore(t, now)
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
			t.Fatalf("client_secret must not be sent for PKCE-only codex web OAuth: %v", gotForm)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"cx-access","refresh_token":"cx-refresh","id_token":"cx-id","scope":"openid email profile offline_access","expires_in":1800,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewCodexWebOAuthExchangerWithClient(client).(codexWebOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	start, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(902), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, validated, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "codex-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "codex-auth-code",
		"redirect_uri":  chatgptOAuthLoopbackRedirect,
		"client_id":     chatgptOAuthClientID,
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
	}
	if validated.Vendor != credentialstore.VendorOpenAI || validated.AuthMode != credentialstore.AuthModeCodexWebOAuth {
		t.Fatalf("validated session vendor/mode=%s/%s want openai/codex_web_oauth", validated.Vendor, validated.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	for key, want := range map[string]string{
		"access_token":           "cx-access",
		"session_token":          "cx-access",
		"refresh_token":          "cx-refresh",
		"id_token":               "cx-id",
		"client_identity_source": codexWebApprovedProfileSource,
		"client_id_source":       codexWebApprovedProfileSource,
		"auth_mode":              credentialstore.AuthModeCodexWebOAuth,
		"oauth_token_endpoint":   chatgptOAuthTokenURL,
		"client_id":              chatgptOAuthClientID,
	} {
		if got := stringFieldFromAny(payload[key]); got != want {
			t.Fatalf("payload[%s]=%q want %q; payload=%v", key, got, want, payload)
		}
	}
	if got := stringFieldFromAny(candidate.RedactedContext["client_identity_source"]); got != codexWebApprovedProfileSource {
		t.Fatalf("redacted context source=%q want %q", got, codexWebApprovedProfileSource)
	}
}

// 缺陷:OpenAI 未返回 refresh_token 时若静默保存 access-only,codex web 账号续期会在
// 短 TTL 后失败(offline_access 期望 refresh)。
// 判别 mutation:删除 refresh_token 非空校验(或把 shape 放宽到 any-token)时,本测试必须变红。
// 判别 fixture:只返回 access_token 的响应必须被拒,区分"要求 refresh"与"任意 token 都收"。
func TestCodexWebOAuthCallbackRejectsAccessOnlyResponse(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 10, 0, 0, time.UTC)
	store, _ := newCodexWebOAuthTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-without-refresh","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewCodexWebOAuthExchangerWithClient(client).(codexWebOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	start, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(903), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, err := exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, start.Session, start.State, "code")
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("err=%v want ErrInvalidTokenShape for access-only codex web token response", err)
	}
	if err == nil || !strings.Contains(err.Error(), "missing refresh_token") {
		t.Fatalf("err=%v want explicit missing refresh_token message", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty after invalid token shape", candidate.Payload)
	}
}

// 缺陷:device-code(codex_cli_oauth)与 web(codex_web_oauth)若被合并到同一 mode key,
// device-code 路径会消失,或 RegisterExchanger 报重复 —— 二者实为不同获取 UX,必须各占一个 key。
// 判别 mutation:把 web exchanger 注册到 codex_cli_oauth(替掉 device-code)时,本测试对
// device-code 仍解析为 device-code 路径的断言必须变红。本测试同时跑两个 mode key 并断言
// 它们产生不同的 exchanger 类型 / flow shape(自证测试)。
func TestCodexModeKeysResolveToDistinctExchangers(t *testing.T) {
	registry := DefaultExchangerRegistry()

	deviceExc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth))
	if !ok {
		t.Fatalf("codex_cli_oauth exchanger missing")
	}
	if _, isDevice := deviceExc.(openAICodexDeviceCodeExchanger); !isDevice {
		t.Fatalf("codex_cli_oauth resolved to %T want device-code exchanger", deviceExc)
	}

	webExc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth))
	if !ok {
		t.Fatalf("codex_web_oauth exchanger missing")
	}
	if _, isWeb := webExc.(codexWebOAuthExchanger); !isWeb {
		t.Fatalf("codex_web_oauth resolved to %T want web exchanger", webExc)
	}

	// flow-shape 区分:web 路径产出 AuthType=pkce + 非空 authorize URL(浏览器登录),
	// 而 device-code 路径产出 AuthType=device_code(无 authorize URL,走 user_code)。
	store, _ := newCodexWebOAuthTestStore(t, time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC))
	webStart, err := webExc.StartOAuthFlow(context.Background(), store, codexWebStartInput(910), OAuthClientConfig{})
	if err != nil {
		t.Fatalf("web StartOAuthFlow: %v", err)
	}
	if webStart.AuthType != AuthTypePKCE || strings.TrimSpace(webStart.AuthorizeURL) == "" {
		t.Fatalf("web start auth_type=%q authorize_url=%q want pkce + non-empty authorize URL", webStart.AuthType, webStart.AuthorizeURL)
	}
	if webStart.AuthType == AuthTypeDeviceCode {
		t.Fatalf("web exchanger produced device_code flow — mode keys collided")
	}
}

// 缺陷:built-in profile 锁死若被放宽,operator 可注入 client_secret / 把 code 重定向
// 到攻击者 https host —— auth-leak(S0/S1);而攻击者 client_id 必须被中和(丢弃回内置),
// 不能透传到 authorize URL。
// 判别 mutation:让 validateBuiltinProfile 跳过 client_secret 校验,或让
// chatgptBuiltinProfileConfig 透传 override.ClientID 时,对应用例必须变红。
// 判别 fixture:override client_id=attacker_app 后 authorize URL 的 client_id 仍须是内置值
// (中和),而 override client_secret / attacker https redirect 必须 ErrFeatureDisabled。
func TestCodexWebStartLocksDownBuiltinProfile(t *testing.T) {
	store, _ := newCodexWebOAuthTestStore(t, time.Date(2026, 5, 27, 11, 30, 0, 0, time.UTC))
	exchanger := newCodexWebOAuthExchanger()

	// client_id override 必须被中和:start 成功,但 authorize URL 仍带内置 client_id。
	neutralized, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(905), OAuthClientConfig{ClientID: "attacker_app"})
	if err != nil {
		t.Fatalf("client_id override should be neutralized, not error: %v", err)
	}
	parsed, err := url.Parse(neutralized.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v", err)
	}
	if got := parsed.Query().Get("client_id"); got != chatgptOAuthClientID {
		t.Fatalf("authorize client_id=%q want built-in %q (attacker value must be dropped)", got, chatgptOAuthClientID)
	}

	// client_secret 注入 + 攻击者 https redirect 必须被 profile 锁死拒绝。
	for _, tc := range []struct {
		name     string
		override OAuthClientConfig
	}{
		{name: "client_secret_injection", override: OAuthClientConfig{ClientSecret: "from-request"}},
		{name: "attacker_https_redirect", override: OAuthClientConfig{RedirectURI: "https://attacker.test/cb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(906), tc.override)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("override=%+v err=%v want ErrFeatureDisabled (profile lockdown)", tc.override, err)
			}
		})
	}

	// 内置(无 override)必须通过,确认拒绝来自 tamper 而非整体禁用。
	if _, err := exchanger.StartOAuthFlow(context.Background(), store, codexWebStartInput(907), OAuthClientConfig{}); err != nil {
		t.Fatalf("canonical built-in codex web profile rejected: %v", err)
	}
}

// 缺陷:codex_web_oauth ModePlan 暴露为可完成的 OAuth mode,但若其 exchanger 注册被删除,
// 回调期才会静默 ErrOAuthExchangerMissing —— boot gate 必须把这种漂移变成 fatal。
// 判别 mutation:注释掉 vendor_exchangers.go 里 codex_web_oauth 的 register 行时,boot gate
// 测试(健康默认)必须变红;本测试还显式构造"plan 在但 exchanger 缺"以确认 gate 真挡。
func TestCodexWebOAuthModeGatedByConsistencyCheck(t *testing.T) {
	if err := ValidateOAuthModeConsistency(DefaultModePlans(), DefaultExchangerRegistry()); err != nil {
		t.Fatalf("healthy default registry rejected by consistency gate: %v", err)
	}
	// plan 存在但 exchanger 缺失 → gate 必须报错且点名 codex_web_oauth。
	plans := []ModePlan{
		oauthPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
	}
	err := ValidateOAuthModeConsistency(plans, NewExchangerRegistry())
	if err == nil {
		t.Fatal("missing codex_web_oauth exchanger passed consistency gate")
	}
	if !strings.Contains(err.Error(), credentialstore.AuthModeCodexWebOAuth) {
		t.Fatalf("err=%v want it to name codex_web_oauth", err)
	}
}

// 缺陷:wiring 自检 helper 若总返回 true,会掩盖生产未注入受控 HTTP client 的启动错误。
// 判别 mutation:让 helper 对 zero-value exchanger 也返回 true 时,本测试必须变红。
func TestIsCodexWebOAuthExchangerWithExplicitClientDistinguishesInjectedClient(t *testing.T) {
	if IsCodexWebOAuthExchangerWithExplicitClient(newCodexWebOAuthExchanger()) {
		t.Fatal("zero-value codex web exchanger reported explicit client")
	}
	if !IsCodexWebOAuthExchangerWithExplicitClient(NewCodexWebOAuthExchangerWithClient(&http.Client{})) {
		t.Fatal("injected client was not detected")
	}
	if IsCodexWebOAuthExchangerWithExplicitClient(NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)) {
		t.Fatal("fake exchanger reported explicit codex web client")
	}
	if IsCodexWebOAuthExchangerWithExplicitClient(NewChatGPTOAuthExchangerWithClient(&http.Client{})) {
		t.Fatal("chatgpt exchanger misidentified as codex web exchanger")
	}
}

// 缺陷:无 store 的 callback 若回退 fake exchanger,会让 OAuth-only 模式直接接收 JSON token。
// 判别 mutation:把 ExchangeOAuthCode 改成 NewPKCEFakeExchanger fallthrough 时,本测试必须变红。
func TestCodexWebOAuthExchangerExchangeOAuthCodeRequiresStore(t *testing.T) {
	exchanger := newCodexWebOAuthExchanger()
	candidate, err := exchanger.ExchangeOAuthCode(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 101, Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexWebOAuth,
	}, `{"access_token":"FAKE","refresh_token":"FAKE-RT"}`)
	if !errors.Is(err, ErrOAuthExchangerMissing) {
		t.Fatalf("err=%v want ErrOAuthExchangerMissing", err)
	}
	if len(candidate.Payload) != 0 {
		t.Fatalf("candidate payload=%s want empty", candidate.Payload)
	}
}

func newCodexWebOAuthTestStore(t *testing.T, now time.Time) (*PostgresSessionStore, *testSessionDB) {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("h", 32)))
	if err != nil {
		t.Fatal(err)
	}
	db := newTestSessionDB(now)
	return NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now }), db
}

func codexWebStartInput(accountID int64) StartInput {
	return StartInput{
		TenantID: 1, ProviderAccountID: accountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexWebOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}
}
