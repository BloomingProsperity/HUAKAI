package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestClaudeAIOAuthTokenEndpointMatchesCurrentApprovedProfile(t *testing.T) {
	const expected = "https://platform.claude.com/v1/oauth/token"
	if claudeAIOAuthTokenURL != expected {
		t.Fatalf("claudeAIOAuthTokenURL=%q want %q", claudeAIOAuthTokenURL, expected)
	}
}

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

// redirect_uri 此前只查非空,管理员 override 的任意 redirect 能进 authorize URL 接走授权码。
// 判别 mutation: 把 validateClaudeAIBuiltinProfileWithHTTPSAdminAllowlist 的 redirect 校验改回
// `strings.TrimSpace(cfg.RedirectURI) == ""` 时,本测试变红 —— 攻击者 redirect 通过校验并建出 flow。
func TestClaudeAIOAuthExchangerRejectsRedirectOverride(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 6, 0, 0, time.UTC)
	store, db := newClaudeAIOAuthTestStore(t, now)
	exchanger := newClaudeAIOAuthExchanger()

	_, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 103,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{RedirectURI: "http://attacker.test/callback"})
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for non-loopback redirect override", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if got := len(db.rows); got != 0 {
		t.Fatalf("stored flows=%d want 0 after rejected redirect override", got)
	}
}

// TestValidateClaudeAIRedirectURI:claude_ai_oauth 是 claude.ai 固定 public client,只注册 loopback
// redirect,故 redirect_uri 仅接受严格 localhost loopback;任意 https host(含 admin callback path)一律
// 拒绝 —— claude.ai 不会接受非 loopback,且 HTTPS admin server callback 缺 flow_id 注入,
// 放出即是一条无法完成的回调路径。HTTPS admin allowlist 对齐留 roadmap。
// 判别 mutation: 删 http 分支任一约束(host/port/path),对应 reject 用例变绿、断言失败。
func TestValidateClaudeAIRedirectURI(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "accept_builtin_loopback", raw: claudeAIOAuthLoopbackRedirect, ok: true},
		{name: "accept_other_localhost_port", raw: "http://localhost:8080/callback", ok: true},
		{name: "reject_attacker_https", raw: "https://attacker.test/cb"},
		{name: "reject_127_loopback", raw: "http://127.0.0.1:54545/callback"},
		{name: "reject_https_admin_callback", raw: "https://huakai.example/admin/v1/credentials/oauth-callback"},
		{name: "reject_loopback_wrong_path", raw: "http://localhost:54545/evil"},
		{name: "reject_loopback_without_port", raw: "http://localhost/callback"},
		{name: "reject_loopback_low_port", raw: "http://localhost:80/callback"},
		{name: "reject_loopback_userinfo", raw: "http://user@localhost:54545/callback"},
		{name: "reject_empty", raw: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClaudeAIRedirectURI(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("validateClaudeAIRedirectURI(%q)=%v want nil", tc.raw, err)
			}
			if !tc.ok && !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("validateClaudeAIRedirectURI(%q)=%v want ErrFeatureDisabled", tc.raw, err)
			}
		})
	}
}

// 缺陷：Anthropic token 端点要求把已校验的回调 state 原样带入 JSON 兑换体，遗漏会使内建 onboarding 失败。
// 判别 mutation：删除兑换体里的 state 后，mock 解码得到空值，与 start.State 不同，本测试立即转红。
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
	}, OAuthClientConfig{})
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
		"redirect_uri":  claudeAIOAuthLoopbackRedirect,
		"client_id":     claudeAIOAuthPublicClientID,
		"code_verifier": start.CodeVerifier,
		"state":         start.State,
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

// TestClaudeAIOAuthExchangeCapturesUpstreamAccountIdentity 是针对实时路径
// Anthropic 账户身份接缝的、有区分力且自证明的守卫：同一段 exchange 代码运行两次，
// 一次 token 响应中存在 account.uuid/email_address（正确路径），一次 account 对象缺失
// （降级路径），并断言两者恰好在所捕获的身份上出现分叉。删掉 anthropic_oauth.go 中的
// AttachIdentity 调用会让正确路径的断言变红，而降级路径仍为绿 —— 没有区分力的 fixture
// 做不到这一点。
func TestClaudeAIOAuthExchangeCapturesUpstreamAccountIdentity(t *testing.T) {
	const (
		wantUUID  = "acct-uuid-7f3a"
		wantEmail = "alice@example.com"
	)
	cases := []struct {
		name       string
		respBody   string
		wantID     string
		wantEmail  string
		wantRedact bool
	}{
		{
			name:       "account_present_captures_identity",
			respBody:   `{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer","account":{"uuid":"` + wantUUID + `","email_address":"` + wantEmail + `"}}`,
			wantID:     wantUUID,
			wantEmail:  wantEmail,
			wantRedact: true,
		},
		{
			name:       "account_absent_falls_back_manual_no_id",
			respBody:   `{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`,
			wantID:     "",
			wantEmail:  "",
			wantRedact: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
			store, _ := newClaudeAIOAuthTestStore(t, now)
			exchanger := claudeAIOAuthExchanger{now: func() time.Time { return now }}
			registry := NewExchangerRegistry()
			if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
				t.Fatalf("RegisterExchanger: %v", err)
			}
			restore := withClaudeAIOAuthRoundTripper(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tc.respBody)),
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
			candidate, _, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "anthropic-auth-code", registry)
			if err != nil {
				t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
			}
			// 两条路径都必须仍能产出可用的凭据 —— 身份捕获只是元数据，
			// 绝不能阻断获取。
			var payload map[string]any
			if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
				t.Fatalf("candidate payload JSON: %v", err)
			}
			if got := stringFieldFromAny(payload["access_token"]); got != "AT" {
				t.Fatalf("access_token=%q want AT (acquisition must succeed on both paths)", got)
			}
			if candidate.ExternalAccountID != tc.wantID {
				t.Fatalf("ExternalAccountID=%q want %q", candidate.ExternalAccountID, tc.wantID)
			}
			if candidate.ExternalAccountEmail != tc.wantEmail {
				t.Fatalf("ExternalAccountEmail=%q want %q", candidate.ExternalAccountEmail, tc.wantEmail)
			}
			gotRedactID, _ := candidate.RedactedContext[RedactedKeyUpstreamAccountID].(string)
			if tc.wantRedact {
				if gotRedactID != tc.wantID {
					t.Fatalf("RedactedContext[%s]=%q want %q", RedactedKeyUpstreamAccountID, gotRedactID, tc.wantID)
				}
			} else if gotRedactID != "" {
				t.Fatalf("RedactedContext[%s]=%q want absent on degraded path", RedactedKeyUpstreamAccountID, gotRedactID)
			}
			// 原始 id_token / account 对象绝不能泄露进 RedactedContext。
			if v, ok := candidate.RedactedContext["account"]; ok {
				t.Fatalf("RedactedContext leaked raw account object: %v", v)
			}
		})
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

// ANT-4 (mimicry transport 注入): NewClaudeAIOAuthExchangerWithClient
// 接受 caller 注入的 *http.Client, exchangeAuthorizationCodeJSON 必须用它,
// 不走全局 http.DefaultClient。生产 wiring 用它接 anthropicoauth.DefaultHTTPClient
// (mimicry uTLS), test 可用它注入 mock 而不污染 http.DefaultTransport。
// 判别 mutation: 把 e.client() 改回 http.DefaultClient → 注入 client 的
// hits 计数停在 0, 该 test 立刻变红。
func TestClaudeAIOAuthExchangerUsesInjectedHTTPClient(t *testing.T) {
	now := time.Date(2026, 5, 26, 14, 0, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)
	var injectedHits int
	injected := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		injectedHits++
		if r.URL.String() != claudeAIOAuthTokenURL {
			t.Fatalf("token URL=%s want %s", r.URL.String(), claudeAIOAuthTokenURL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := NewClaudeAIOAuthExchangerWithClient(injected).(claudeAIOAuthExchanger)
	exchanger.now = func() time.Time { return now }

	// 把 http.DefaultTransport 设为 panic-on-call trip; 若 exchanger 错走
	// http.DefaultClient 而非 injected, 这条 transport 会被命中并 fail。
	defer func(old http.RoundTripper) { http.DefaultTransport = old }(http.DefaultTransport)
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("exchanger 错走 http.DefaultTransport, 注入的 client 未生效")
		return nil, errors.New("unreachable")
	})

	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 999,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	_, _, err = CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "ant4-real-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if injectedHits != 1 {
		t.Fatalf("injected client hits=%d want 1 (exchanger 没用注入 client)", injectedHits)
	}
}

// validateOAuthEndpointURL 只做字面 URL 检查, DNS-rebind 攻击 (https://attacker.example
// 但 DNS 解到 127.0.0.1) 静态层抓不住。深层 dial-time guard 通过
// auth.NewSSRFProtectedOAuthClient 在 transport.DialContext 校验目标 IP,
// 必拒内网 / metadata / loopback。判别 mutation: 撤回
// `client = auth.NewSSRFProtectedOAuthClient(http.DefaultClient)` 改回
// `client = http.DefaultClient`, 此 test 看到 DNS-rebind 实际 dial 命中
// 127.0.0.1 (无连接) 而不是 oauth_endpoint_blocked, 立刻变红。
func TestAuthorizationCodeExchangeDeepDNSRebindIsBlocked(t *testing.T) {
	// caller 不注入 custom client → 走 auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	// 生产路径。
	exchanger := authorizationCodeOAuthExchanger{}
	restore := auth.SwapOAuthIPLookupForTesting(func(_ context.Context, host string) ([]net.IPAddr, error) {
		// 模拟 attacker.example 域 DNS 解析返回 loopback (经典 DNS-rebind)。
		_ = host
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	defer restore()

	payload := storedPKCEPayload{
		CodeVerifier: "verifier",
		TokenURL:     "https://attacker.example/token",
		ClientID:     "cid",
		RedirectURI:  "https://huakai.example.test/callback",
	}
	_, err := exchanger.exchangeAuthorizationCode(context.Background(), payload, "code")
	if err == nil {
		t.Fatal("DNS-rebind attack went through; deep SSRF guard 失效")
	}
	// auth.ErrOAuthEndpointBlocked sentinel 暴露在 err 链中。
	if !errors.Is(err, auth.ErrOAuthEndpointBlocked) {
		t.Fatalf("err=%v want auth.ErrOAuthEndpointBlocked", err)
	}
}

// 校验只看非空,没 enforce scheme/host;caller 可写 http:// 或 127.0.0.1 或
// metadata IP 让 client_secret/code/verifier 漏到攻击者地址。新加
// validateOAuthEndpointURL 做静态闸门。
// 判别 mutation: 删 validateOAuthEndpointURL 调用 → test 立刻接受 attacker URL 红。
func TestOperatorOAuthConfigRejectsSSRFEndpoints(t *testing.T) {
	cases := []struct {
		name, authURL, tokenURL string
	}{
		{name: "http_scheme", authURL: "http://attacker.example/authorize", tokenURL: "https://attacker.example/token"},
		{name: "loopback_host", authURL: "https://attacker.example/authorize", tokenURL: "https://127.0.0.1/token"},
		{name: "private_net", authURL: "https://192.168.1.10/authorize", tokenURL: "https://api.example/token"},
		{name: "localhost_name", authURL: "https://api.example/authorize", tokenURL: "https://localhost/token"},
		{name: "metadata_ip", authURL: "https://api.example/authorize", tokenURL: "https://169.254.169.254/token"},
		{name: "metadata_dns", authURL: "https://metadata.google.internal/authorize", tokenURL: "https://api.example/token"},
		{name: "link_local", authURL: "https://api.example/authorize", tokenURL: "https://[fe80::1]/token"},
		{name: "data_url", authURL: "data:text/plain,attacker", tokenURL: "https://api.example/token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := OAuthClientConfig{
				Source: ClientSourceOperatorConfig, ClientID: "cid",
				AuthURL: tc.authURL, TokenURL: tc.tokenURL,
				RedirectURI: "https://huakai.example.test/callback",
				Scopes:      []string{"profile"},
			}
			err := validateOperatorPKCEConfig("gemini", "oauth", cfg)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("err=%v want ErrFeatureDisabled for SSRF-suspicious endpoint", err)
			}
		})
	}

	// 反向控制: 合法 operator 配置 (https + 公网 host) 必须通过, 否则我们是过严。
	ok := OAuthClientConfig{
		Source: ClientSourceOperatorConfig, ClientID: "cid",
		AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
		RedirectURI: "https://huakai.example.test/callback",
		Scopes:      []string{"profile"},
	}
	if err := validateOperatorPKCEConfig("gemini", "oauth", ok); err != nil {
		t.Fatalf("合法 operator OAuth 配置被错误拒绝: %v", err)
	}
}

// google_one) 可被 caller 传 flow_kind=paste 直接 finalize 绕过 OAuth。
// CreateFromStart 必须 enforce ModePlan.AllowedHelpers 白名单。
// 判别 mutation: 删 CreateFromStart 中的 flowKindAllowed 检查, 该 test
// 立即变红 — chatgpt_oauth 接受 paste session 创建, 漏洞复现。
func TestOAuthOnlyModeRejectsPasteSessionStart(t *testing.T) {
	now := time.Date(2026, 5, 26, 15, 0, 0, 0, time.UTC)
	store, _ := newClaudeAIOAuthTestStore(t, now)

	cases := []struct {
		name, vendor, authMode string
		flowKind               FlowKind
	}{
		{name: "openai_chatgpt_oauth_paste", vendor: credentialstore.VendorOpenAI, authMode: credentialstore.AuthModeChatGPTOAuth, flowKind: FlowKindPaste},
		{name: "openai_chatgpt_oauth_cli_import", vendor: credentialstore.VendorOpenAI, authMode: credentialstore.AuthModeChatGPTOAuth, flowKind: FlowKindCLIImport},
		{name: "gemini_code_assist_paste", vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeCodeAssist, flowKind: FlowKindPaste},
		{name: "gemini_google_one_json_import", vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeGoogleOne, flowKind: FlowKindJSONImport},
		// claude_ai_oauth 现为 OAuth-only,paste 旁路必须被拒。
		// 判别 mutation: 把 types.go 的 AllowedHelpers 改回 {FlowKindOAuth, FlowKindPaste},此用例立即变红
		// (paste START 被接受,任意 Anthropic token 注入旁路复现)。
		{name: "anthropic_claude_ai_oauth_paste", vendor: credentialstore.VendorAnthropic, authMode: credentialstore.AuthModeClaudeAIOAuth, flowKind: FlowKindPaste},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.CreateFromStart(context.Background(), StartInput{
				TenantID: 1, ProviderAccountID: 777,
				Vendor: tc.vendor, AuthMode: tc.authMode, Kind: tc.flowKind,
				ActorID: "owner", ActorRole: "platform_admin",
			})
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("err=%v want ErrFeatureDisabled; OAuth-only 模式不应接受 %s 绕过", err, tc.flowKind)
			}
		})
	}

	// 反向控制: 同样 vendor/auth_mode 但用合法 flow_kind=oauth 必须通过 (start 进入
	// StatusStarted), 否则我们是过严而不是分辨真假。
	if _, err := store.CreateFromStart(context.Background(), StartInput{
		TenantID: 1, ProviderAccountID: 778,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Kind: FlowKindOAuth, ActorID: "owner", ActorRole: "platform_admin",
	}); err != nil {
		t.Fatalf("合法 OAuth 路径被错误拒绝: %v", err)
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
