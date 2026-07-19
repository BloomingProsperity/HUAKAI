package credentialacq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestNormalizedTokenPayloadPreservesSubscriptionEvidence(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{
		"sub":"user-1",
		"email":"user@example.test",
		"https://api.openai.com/auth":{
			"chatgpt_plan_type":"Plus",
			"chatgpt_account_id":"account-1"
		}
	}`))
	idToken := "e30." + claims + ".signature"
	raw, err := normalizedTokenPayload(map[string]any{
		"refreshToken": "refresh-1",
		"idToken":      idToken,
		"tokenType":    "Bearer",
		"plan_type":    "Free",
		"accountId":    "stale-account",
		"expiresIn":    3600,
	}, "access-1")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"refresh_token":      "refresh-1",
		"id_token":           idToken,
		"token_type":         "Bearer",
		"chatgpt_plan_type":  "Free",
		"chatgpt_account_id": "stale-account",
	} {
		if got := stringField(payload, key); got != want {
			t.Fatalf("payload[%s]=%q，期望 %q；payload=%v", key, got, want, payload)
		}
	}
	candidate := candidateFromDeviceTokenPayload(Session{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
	}, raw)
	if candidate.Subscription.Label() != "openai:plus" || candidate.Subscription.Source != "id_token_claim" {
		t.Fatalf("令牌声明必须胜过响应体旧套餐：%+v", candidate.Subscription)
	}
	if candidate.ExternalAccountID != "account-1" || candidate.ExternalSubjectID != "user-1" || candidate.ExternalAccountEmail != "user@example.test" {
		t.Fatalf("device-code 身份提取错误：%+v", candidate)
	}
}

func TestDeviceCodePollHonorsSlowDown(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 10, 0, 0, time.UTC)
	var pollCount atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/device":
			return jsonHTTPResponse(t, map[string]any{
				"device_code":      "dev-123",
				"user_code":        "ABCD-EFGH",
				"verification_uri": "https://github.example.test/login/device",
				"expires_in":       900,
				"interval":         5,
			}), nil
		case "/token":
			switch pollCount.Add(1) {
			case 1:
				return jsonHTTPResponse(t, map[string]any{"error": "authorization_pending"}), nil
			case 2:
				return jsonHTTPResponse(t, map[string]any{"error": "slow_down"}), nil
			default:
				return jsonHTTPResponse(t, map[string]any{
					"access_token":  "gho-access",
					"refresh_token": "gho-refresh",
					"expires_in":    3600,
				}), nil
			}
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		}
	})}

	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now }))
	start, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "fake-client", AuthURL: "https://fake.copilot.local/device", TokenURL: "https://fake.copilot.local/token",
		Scopes: []string{"openid", "offline_access"}, Source: ClientSourceOperatorConfig, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.AuthType != AuthTypeDeviceCode {
		t.Fatalf("auth_type=%q want %q", start.AuthType, AuthTypeDeviceCode)
	}
	if start.UserCode != "ABCD-EFGH" || start.VerificationURI == "" {
		t.Fatalf("device display fields user_code=%q verification_uri=%q", start.UserCode, start.VerificationURI)
	}

	var sleeps []time.Duration
	candidate, err := PollDeviceCodeToken(context.Background(), start.Session, OAuthClientConfig{TokenURL: "https://fake.copilot.local/token", ClientID: "fake-client"},
		WithDeviceCodeHTTPClient(client),
		WithDeviceCodeNow(func() time.Time { return now }),
		WithDeviceCodeSleeper(func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("PollDeviceCodeToken: %v", err)
	}
	if len(sleeps) != 2 {
		t.Fatalf("sleep count=%d want 2 (%v)", len(sleeps), sleeps)
	}
	if sleeps[0] != 5*time.Second || sleeps[1] != 10*time.Second {
		t.Fatalf("poll intervals=%v want [5s 10s]", sleeps)
	}
	if !(sleeps[1] > sleeps[0]) {
		t.Fatal("fixture is not discriminating: slow_down did not require a longer interval")
	}
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("candidate target=%s/%s", candidate.Vendor, candidate.AuthMode)
	}
	if !json.Valid(candidate.Payload) {
		t.Fatalf("candidate payload is not JSON: %s", string(candidate.Payload))
	}
}

func TestDeviceCodeSingleAttemptReturnsPendingWithoutSleeping(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		session  Session
		response *http.Response
	}{
		{
			name: "标准设备码",
			session: Session{
				TenantID: 1, ProviderAccountID: 2, Vendor: credentialstore.VendorKimi,
				AuthMode: credentialstore.AuthModeKimiOAuth, AuthType: AuthTypeDeviceCode,
				DeviceCodePayload: map[string]any{
					"device_code": "dev", "token_url": "https://auth.example.test/token", "client_id": "client",
					"issued_at": now.Format(time.RFC3339Nano), "expires_in": 900, "interval": 5,
				},
			},
			response: jsonHTTPResponse(t, map[string]any{"error": "authorization_pending"}),
		},
		{
			name: "Codex 设备码",
			session: Session{
				TenantID: 1, ProviderAccountID: 3, Vendor: credentialstore.VendorOpenAI,
				AuthMode: credentialstore.AuthModeCodexCLIOAuth, AuthType: AuthTypeDeviceCode,
				DeviceCodePayload: map[string]any{
					"device_auth_id": "device-auth", "user_code": "USER", "token_url": "https://auth.example.test/device-token",
					"oauth_token_url": "https://auth.example.test/oauth-token", "client_id": "client",
					"issued_at": now.Format(time.RFC3339Nano), "expires_in": 900, "interval": 5,
				},
			},
			response: &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls, sleeps atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return tc.response, nil
			})}
			_, err := PollDeviceCodeToken(context.Background(), tc.session, OAuthClientConfig{},
				WithDeviceCodeHTTPClient(client),
				WithDeviceCodeNow(func() time.Time { return now }),
				WithDeviceCodeSingleAttempt(),
				WithDeviceCodeSleeper(func(context.Context, time.Duration) error {
					sleeps.Add(1)
					return nil
				}),
			)
			if !errors.Is(err, ErrDevicePollPending) || DevicePollRetryAfter(err) != 5*time.Second {
				t.Fatalf("err=%v retry=%s", err, DevicePollRetryAfter(err))
			}
			if calls.Load() != 1 || sleeps.Load() != 0 {
				t.Fatalf("calls=%d sleeps=%d want 1/0", calls.Load(), sleeps.Load())
			}
		})
	}
}

func TestSSOPollGivesUpAtExpiresInBoundary(t *testing.T) {
	startTime := time.Date(2026, 5, 24, 9, 20, 0, 0, time.UTC)
	now := startTime
	expiresAt := startTime.Add(5 * time.Minute)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/sso/start":
			return jsonHTTPResponse(t, map[string]any{
				"deviceCode":      "sso-dev-123",
				"userCode":        "KIRO-CODE",
				"verificationUri": "https://device.sso.example.test/",
				"expiresIn":       300,
				"interval":        5,
			}), nil
		case "/sso/token":
			return jsonHTTPResponse(t, map[string]any{"error": "authorization_pending"}), nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		}
	})}

	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(startTime)).WithNow(func() time.Time { return now }))
	start, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 3,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeBedrock,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "fake-sso-client", AuthURL: "https://fake.kiro.local/sso/start", TokenURL: "https://fake.kiro.local/sso/token",
		Source: ClientSourceOperatorConfig, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.AuthType != AuthTypeSSO {
		t.Fatalf("auth_type=%q want %q", start.AuthType, AuthTypeSSO)
	}

	totalSlept := time.Duration(0)
	_, err = PollSSOToken(context.Background(), start.Session, OAuthClientConfig{TokenURL: "https://fake.kiro.local/sso/token", ClientID: "fake-sso-client"},
		WithDeviceCodeHTTPClient(client),
		WithDeviceCodeNow(func() time.Time { return now }),
		WithDeviceCodeSleeper(func(_ context.Context, d time.Duration) error {
			now = now.Add(d)
			totalSlept += d
			if now.After(expiresAt) {
				return errors.New("poll continued after expires_in boundary")
			}
			return nil
		}),
	)
	if !errors.Is(err, ErrFlowExpired) {
		t.Fatalf("err=%v want %v", err, ErrFlowExpired)
	}
	if totalSlept != 5*time.Minute {
		t.Fatalf("total sleep=%s want 5m", totalSlept)
	}
}

func TestOAuthExchangerRegistryRejectsWrongVendorTokenShape(t *testing.T) {
	session := Session{
		TenantID: 1, ProviderAccountID: 4,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "admin-1",
	}
	anthropicToken := `{"access_token":"anthropic-access","refresh_token":"anthropic-refresh"}`

	correct := NewExchangerRegistry()
	if err := correct.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), NewPKCEFakeExchanger(TokenShapeAccessRefresh)); err != nil {
		t.Fatalf("register correct exchanger: %v", err)
	}
	if _, err := correct.Exchange(context.Background(), session, anthropicToken); err != nil {
		t.Fatalf("correct exchanger rejected anthropic token: %v", err)
	}

	wrong := NewExchangerRegistry()
	if err := wrong.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), NewPKCEFakeExchanger(TokenShapeSession)); err != nil {
		t.Fatalf("register wrong exchanger: %v", err)
	}
	_, err := wrong.Exchange(context.Background(), session, anthropicToken)
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("err=%v want %v", err, ErrInvalidTokenShape)
	}
}

func TestDefaultExchangerRegistryHasOpenAICodexDeviceCodeAliases(t *testing.T) {
	// 回归保护:调用方可以用 provider-code 别名定位 OpenAI Codex device-code flow。
	// 变异自检:移除任一别名都会使此 lookup 失败,而旧的 openai/codex_cli_oauth 键仍能通过。
	registry := DefaultExchangerRegistry()
	for _, name := range []string{
		"openai/codex_cli_oauth",
		"openai_codex/device-code",
		"openai_codex/device_code",
	} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("Lookup(%q) missing", name)
		}
	}
}

func TestKimiDeviceExchangerRegistered(t *testing.T) {
	// 变异:删除 DefaultExchangerRegistry 中的 kimi/kimi_oauth 注册;
	// 此 lookup 必须变红,而无关的 device-code 别名仍能通过。
	registry := DefaultExchangerRegistry()
	if _, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth)); !ok {
		t.Fatalf("missing Kimi device-code exchanger for %s", credentialstore.ModeKey(credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth))
	}
}

func TestKimiDeviceConfigConstants(t *testing.T) {
	// 变异:改动 Kimi 的 client_id、device endpoint、token endpoint 或
	// device-code grant 字符串;下方 start/poll 请求断言必须变红。
	now := time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC)
	var startURL string
	var startClientID string
	var pollGrantType string
	var pollClientID string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("request body is not JSON: %s", string(raw))
		}
		switch r.URL.String() {
		case "https://auth.kimi.com/api/oauth/device_authorization":
			startURL = r.URL.String()
			startClientID = stringField(body, "client_id")
			return jsonHTTPResponse(t, map[string]any{
				"device_code":      "kimi-device-code",
				"user_code":        "KIMI-CODE",
				"verification_uri": "https://auth.kimi.com/device",
				"expires_in":       900,
				"interval":         1,
			}), nil
		case "https://auth.kimi.com/api/oauth/token":
			pollClientID = stringField(body, "client_id")
			pollGrantType = stringField(body, "grant_type")
			return jsonHTTPResponse(t, map[string]any{
				"access_token":  "kimi-access",
				"refresh_token": "kimi-refresh",
				"expires_in":    3600,
			}), nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		}
	})}
	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now }))

	start, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 8,
		Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeKimiOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{HTTPClient: client})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if startURL != "https://auth.kimi.com/api/oauth/device_authorization" {
		t.Fatalf("device authorization URL=%q", startURL)
	}
	if startClientID != "17e5f671-d194-4dfb-9706-5516cb48c098" {
		t.Fatalf("device authorization client_id=%q", startClientID)
	}
	if start.AuthType != AuthTypeDeviceCode || start.UserCode != "KIMI-CODE" {
		t.Fatalf("auth_type=%q user_code=%q", start.AuthType, start.UserCode)
	}
	if start.Session.Vendor != credentialstore.VendorKimi || start.Session.AuthMode != credentialstore.AuthModeKimiOAuth {
		t.Fatalf("session mode=%s/%s", start.Session.Vendor, start.Session.AuthMode)
	}

	candidate, err := PollDeviceCodeToken(context.Background(), start.Session, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client),
		WithDeviceCodeNow(func() time.Time { return now }),
		WithDeviceCodeSleeper(func(context.Context, time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatalf("PollDeviceCodeToken: %v", err)
	}
	if pollClientID != "17e5f671-d194-4dfb-9706-5516cb48c098" {
		t.Fatalf("token poll client_id=%q", pollClientID)
	}
	if pollGrantType != "urn:ietf:params:oauth:grant-type:device_code" {
		t.Fatalf("token poll grant_type=%q", pollGrantType)
	}
	if candidate.Vendor != credentialstore.VendorKimi || candidate.AuthMode != credentialstore.AuthModeKimiOAuth {
		t.Fatalf("candidate mode=%s/%s", candidate.Vendor, candidate.AuthMode)
	}
}

func TestKimiSSRFHost(t *testing.T) {
	// 变异:移除 Kimi 端点 host 校验或放行任意公网 HTTPS host;
	// 此伪造的攻击者端点将被调用,测试随之变红。
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return jsonHTTPResponse(t, map[string]any{
			"device_code":      "attacker-device-code",
			"user_code":        "ATTACKER",
			"verification_uri": "https://attacker.example.com/device",
		}), nil
	})}
	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(time.Date(2026, 6, 7, 9, 10, 0, 0, time.UTC))))

	_, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 9,
		Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeKimiOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		TokenURL:   "https://attacker.example.com/oauth/token",
		HTTPClient: client,
	})
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("StartOAuthFlow err=%v, want ErrFeatureDisabled for non-kimi.com token host", err)
	}
	if called {
		t.Fatal("Kimi device-code start called HTTP after non-kimi.com token endpoint override")
	}
}

func TestOpenAICodexDeviceCodeStartRequiresOperatorConfig(t *testing.T) {
	// 回归保护:已注册的 OpenAI Codex device-code exchanger
	// 必须在任何 HTTP 调用前强制 operator_config。变异自检:
	// 若直接委派给通用 device-code exchanger,就会调用此
	// client 并接受 public_cli_client。
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return jsonHTTPResponse(t, map[string]any{}), nil
	})}
	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(time.Date(2026, 5, 24, 14, 0, 0, 0, time.UTC))))

	_, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 5,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://operator.openai.example.test/device", TokenURL: "https://operator.openai.example.test/token",
		ClientID: "operator-client", Scopes: []string{"openid", "offline_access"},
		Source: ClientSourcePublicCLI, HTTPClient: client,
	})
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("StartOAuthFlow err=%v, want ErrFeatureDisabled", err)
	}
	if called {
		t.Fatal("OpenAI Codex device-code start called HTTP before operator_config validation")
	}
}

func TestOpenAICodexDeviceCodeAliasCanonicalizesCredentialMode(t *testing.T) {
	// 回归保护:provider-code 别名必须以规范的 credentialstore vendor/auth_mode
	// 创建并轮询,使最终化(finalization)能够校验结果。
	now := time.Date(2026, 5, 24, 14, 10, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/device":
			return jsonHTTPResponse(t, map[string]any{
				"device_code":      "codex-dev-123",
				"user_code":        "OPENAI-CODEX",
				"verification_uri": "https://auth.openai.example.test/activate",
				"expires_in":       900,
				"interval":         5,
			}), nil
		case "/token":
			return jsonHTTPResponse(t, map[string]any{
				"access_token":  "codex-access",
				"refresh_token": "codex-refresh",
				"expires_in":    3600,
			}), nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		}
	})}
	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now }))

	start, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 6,
		Vendor: "openai_codex", AuthMode: "device_code",
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://operator.openai.example.test/device", TokenURL: "https://operator.openai.example.test/token",
		ClientID: "operator-client", Scopes: []string{"openid", "offline_access"},
		Source: ClientSourceOperatorConfig, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.Session.Vendor != credentialstore.VendorOpenAI || start.Session.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("session mode=%s/%s, want openai/codex_cli_oauth", start.Session.Vendor, start.Session.AuthMode)
	}
	if start.Session.ClientIdentitySource != ClientSourceOperatorConfig {
		t.Fatalf("client source=%q, want operator_config", start.Session.ClientIdentitySource)
	}
	candidate, err := PollDeviceCodeToken(context.Background(), start.Session, OAuthClientConfig{
		TokenURL: "https://operator.openai.example.test/token", ClientID: "operator-client",
	}, WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSleeper(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatalf("PollDeviceCodeToken: %v", err)
	}
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("candidate mode=%s/%s, want openai/codex_cli_oauth", candidate.Vendor, candidate.AuthMode)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
}

func TestOpenAICodexDeviceFlowPollsOpenAIDeviceAuthThenExchangesAuthorizationCode(t *testing.T) {
	now := time.Date(2026, 5, 25, 8, 20, 0, 0, time.UTC)
	pollCount := 0
	var exchangeBody string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			return jsonHTTPResponse(t, map[string]any{
				"device_auth_id": "dev-auth-123",
				"user_code":      "OPENAI-CODE",
				"interval":       "1",
			}), nil
		case "/api/accounts/deviceauth/token":
			pollCount++
			if pollCount == 1 {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":"pending"}`)),
				}, nil
			}
			return jsonHTTPResponse(t, map[string]any{
				"authorization_code": "oauth-code-from-device",
				"code_verifier":      "verifier-from-device",
				"code_challenge":     "challenge-from-device",
			}), nil
		case "/oauth/token":
			raw, _ := io.ReadAll(r.Body)
			exchangeBody = string(raw)
			return jsonHTTPResponse(t, map[string]any{
				"access_token":  "codex-access-from-exchange",
				"refresh_token": "codex-refresh-from-exchange",
				"expires_in":    3600,
			}), nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		}
	})}
	store := withTestSessionKeys(t, NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now }))

	start, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 7,
		Vendor: "openai_codex", AuthMode: "device_code",
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://auth.openai.test/api/accounts/deviceauth/usercode", TokenURL: "https://auth.openai.test/api/accounts/deviceauth/token",
		ClientID: "operator-client", RedirectURI: "https://auth.openai.com/deviceauth/callback",
		Scopes: []string{"openid", "email", "profile", "offline_access"},
		Source: ClientSourceOperatorConfig, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.UserCode != "OPENAI-CODE" || start.VerificationURI == "" {
		t.Fatalf("device display user_code=%q verification=%q", start.UserCode, start.VerificationURI)
	}

	candidate, err := PollDeviceCodeToken(context.Background(), start.Session, OAuthClientConfig{
		TokenURL: "https://auth.openai.test/api/accounts/deviceauth/token",
		ClientID: "operator-client", RedirectURI: "https://auth.openai.com/deviceauth/callback",
	}, WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSleeper(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatalf("PollDeviceCodeToken: %v", err)
	}
	if pollCount != 2 {
		t.Fatalf("pollCount=%d want 2", pollCount)
	}
	form, err := url.ParseQuery(exchangeBody)
	if err != nil {
		t.Fatalf("ParseQuery exchangeBody=%q: %v", exchangeBody, err)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "operator-client",
		"code":          "oauth-code-from-device",
		"redirect_uri":  "https://auth.openai.com/deviceauth/callback",
		"code_verifier": "verifier-from-device",
	} {
		if got := form.Get(key); got != want {
			t.Fatalf("exchange form[%s]=%q want %q; form=%v", key, got, want, form)
		}
	}
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("candidate mode=%s/%s, want openai/codex_cli_oauth", candidate.Vendor, candidate.AuthMode)
	}
	if !strings.Contains(string(candidate.Payload), "codex-refresh-from-exchange") {
		t.Fatalf("payload=%s, want exchanged refresh token", string(candidate.Payload))
	}
}

func TestPostFormJSONRejectsOversizedResponse(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		body := `{"access_token":"` + strings.Repeat("a", 100*1024) + `"}`
		if calls == 1 {
			body = `{"access_token":"` + strings.Repeat("b", 1100*1024) + `"}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	var tooLarge map[string]any
	_, err := postFormJSON(context.Background(), client, "https://oauth.example.test/token", url.Values{"grant_type": {"authorization_code"}}, &tooLarge)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversized form response err=%v want ErrResponseTooLarge", err)
	}

	var accepted map[string]any
	_, err = postFormJSON(context.Background(), client, "https://oauth.example.test/token", url.Values{"grant_type": {"authorization_code"}}, &accepted)
	if err != nil {
		t.Fatalf("100KB form response err=%v", err)
	}
	if got := stringField(accepted, "access_token"); len(got) != 100*1024 {
		t.Fatalf("accepted access token length=%d want %d", len(got), 100*1024)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(t *testing.T, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal JSON response: %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}
