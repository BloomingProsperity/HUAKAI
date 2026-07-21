package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	appconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	providerantigravity "github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
)

func TestDefaultModeAdapterRegistryCoversCredentialStoreModes(t *testing.T) {
	registry := DefaultModeAdapterRegistry()
	wantCount := len(credentialstore.DefaultHandlerRegistry().Names())
	if got := registry.Names(); len(got) != wantCount {
		t.Fatalf("mode adapter count=%d want %d: %v", len(got), wantCount, got)
	}
	for _, key := range credentialstore.DefaultHandlerRegistry().Names() {
		vendor, mode := splitCredentialModeKey(key)
		if _, ok := registry.Lookup(vendor, mode); !ok {
			t.Fatalf("missing mode refresh adapter %s", key)
		}
	}
}

func TestClaudeSetupTokenModeIsExplicitlyStatic(t *testing.T) {
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken)
	if !ok {
		t.Fatal("Claude Setup Token mode adapter missing")
	}
	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeSetupToken,
		Payload: []byte(`{"setup_token":"static-secret"}`),
	})
	if !errors.Is(err, ErrNoRefreshRequired) || len(result.Payload) != 0 || !result.AccessExpiresAt.IsZero() {
		t.Fatalf("result=%+v err=%v，期望静态凭据明确跳过刷新", result, err)
	}
}

func TestDefaultModeAdapterRegistryRoutesSlice26OAuthModes(t *testing.T) {
	registry := DefaultModeAdapterRegistry()
	cases := []struct {
		vendor   string
		authMode string
		key      string
		assert   func(*testing.T, ModeRefreshAdapter)
	}{
		{credentialstore.VendorGemini, credentialstore.AuthModeOAuth, "gemini/oauth", assertOperatorBoundOAuthModeAdapter},
		{credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth, "antigravity/oauth", assertLegacyOAuthModeAdapter},
		{credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth, "windsurf/oauth", assertWindsurfManualModeAdapter},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			adapter, ok := registry.Lookup(tc.vendor, tc.authMode)
			if !ok || adapter == nil {
				t.Fatalf("missing mode refresh adapter %s", tc.key)
			}
			tc.assert(t, adapter)
		})
	}
}

func assertLegacyOAuthModeAdapter(t *testing.T, adapter ModeRefreshAdapter) {
	t.Helper()
	if _, ok := adapter.(legacyOAuthModeAdapter); !ok {
		t.Fatalf("adapter type=%T，期望 legacyOAuthModeAdapter", adapter)
	}
}

func TestModeRefreshWorkerFindsWindsurfOAuthAdapter(t *testing.T) {
	// 修掉的回归:windsurf/oauth 凭据可以被存储,但 refresh worker 的 mode registry
	// 缺少对应的执行器,从而把账号标为 adapter_missing。Mutation 自检:删掉
	// windsurf/oauth 的默认注册会让本测试以 failure:88:adapter_missing 失败。
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 88, TenantID: 1, ProviderAccountID: 188,
			Vendor: credentialstore.VendorWindsurf, AuthMode: credentialstore.AuthModeOAuth,
			CredentialVersion: 4, PlaintextPayload: []byte(`{"session_token":"windsurf-session","token_source":"windsurf_show_auth_token"}`),
		},
	}
	refresher := &AccountCredentialRefresher{store: store, registry: DefaultModeAdapterRegistry(), now: func() time.Time {
		return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	}}

	err := refresher.Refresh(context.Background(), 188)
	if err != nil {
		t.Fatalf("Refresh returned %v, want no adapter_missing for windsurf/oauth", err)
	}
	// TOKLIFE-04:ErrNoRefreshRequired 现在通过 SetNextAttemptThrottle 设置
	// next_attempt_at,以防止紧密的重试循环;调用序列中预期出现 throttle:88。
	want := []string{"probe", "tx_begin", "throttle:88"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func TestProviderFailureCooldown(t *testing.T) {
	cases := []struct {
		vendor string
		want   time.Duration
	}{
		{vendor: "anthropic", want: time.Minute},
		{vendor: "openai", want: time.Minute},
		{vendor: "gemini", want: time.Minute},
		{vendor: "unknown", want: time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.vendor, func(t *testing.T) {
			if got := providerFailureCooldown(tc.vendor); got != tc.want {
				t.Fatalf("providerFailureCooldown(%q)=%s, want %s", tc.vendor, got, tc.want)
			}
		})
	}
}

func assertOperatorBoundOAuthModeAdapter(t *testing.T, adapter ModeRefreshAdapter) {
	t.Helper()
	if _, ok := adapter.(legacyOAuthModeAdapter); ok {
		t.Fatalf("adapter type=%T must not be legacyOAuthModeAdapter", adapter)
	}
	if got := reflect.TypeOf(adapter).String(); got != "credentialworker.operatorOAuthModeAdapter" {
		t.Fatalf("adapter type=%s, want credentialworker.operatorOAuthModeAdapter", got)
	}
}

func assertWindsurfManualModeAdapter(t *testing.T, adapter ModeRefreshAdapter) {
	t.Helper()
	if got := reflect.TypeOf(adapter).String(); got != "credentialworker.windsurfManualModeAdapter" {
		t.Fatalf("adapter type=%s, want credentialworker.windsurfManualModeAdapter", got)
	}
}

func TestDefaultModeAdapterRegistryRefreshesCodexWithBuiltinPublicProfile(t *testing.T) {
	// 官方 auth.json 与公开 CLI 登录不需要额外运维配置，同时凭据 payload 中伪造的
	// endpoint/client/scope 不能改变续期身份。
	var gotURL string
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":1800}`), nil
	})}
	adapter, ok := newDefaultModeAdapterRegistryWithProjectResolver(client, nil, nil).Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if !ok {
		t.Fatal("Codex CLI OAuth mode adapter missing")
	}
	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 42,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(`{
			"refresh_token":"rt-old",
			"client_id":"credential-cid",
			"scope":"credential-scope",
			"oauth_token_endpoint":"http://evil.attacker.test/token"
		}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotURL != credentialacq.OpenAICodexOAuthTokenURL || gotForm.Get("client_id") != credentialacq.OpenAICodexOAuthClientID || gotForm.Get("scope") != credentialacq.OpenAICodexOAuthRefreshScope {
		t.Fatalf("refresh request url=%q form=%v", gotURL, gotForm)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["access_token"] != "access-new" || payload["session_token"] != "access-new" || payload["refresh_token"] != "refresh-new" {
		t.Fatalf("刷新后运行材料未同步：%v", payload)
	}
	if _, exists := payload["oauth_token_endpoint"]; exists {
		t.Fatalf("输入 endpoint 未被清除：%v", payload)
	}
}

func TestConfiguredModeAdapterRegistryDoesNotOverridePublicCodexIdentity(t *testing.T) {
	var gotURL string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(`{"access_token":"access-new","expires_in":1800}`), nil
	})}
	registry := newDefaultModeAdapterRegistryWithProjectResolver(client, nil, appconfig.VendorOAuthConfigs{
		appconfig.VendorOAuthOpenAICodex: {TokenURL: "https://operator.example.test/token", ClientID: "operator-client"},
	})
	adapter, ok := registry.Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if !ok {
		t.Fatal("Codex CLI OAuth mode adapter missing")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 45, Vendor: credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload:  []byte(`{"access_token":"old","refresh_token":"refresh-old","client_id_source":"public_cli_client"}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotURL != credentialacq.OpenAICodexOAuthTokenURL {
		t.Fatalf("公开 Codex 身份被运维配置覆盖，url=%q", gotURL)
	}
}

func TestConfiguredModeAdapterRegistryRefreshesCodexWithRuntimeConfig(t *testing.T) {
	var gotURL string
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":1800}`), nil
	})}
	configs := appconfig.VendorOAuthConfigs{
		appconfig.VendorOAuthOpenAICodex: {
			AuthURL: "https://operator.example.test/device", TokenURL: "https://operator.example.test/token",
			ClientID: "operator-client", Scope: "openid offline_access",
		},
	}
	registry := newDefaultModeAdapterRegistryWithProjectResolver(client, nil, configs)
	adapter, ok := registry.Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if !ok {
		t.Fatal("Codex CLI OAuth mode adapter missing")
	}
	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 43,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(`{
			"access_token":"access-old",
			"session_token":"access-old",
			"refresh_token":"refresh-old",
			"client_id":"attacker-client",
			"scope":"attacker-scope",
			"oauth_token_endpoint":"https://attacker.test/token"
		}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotURL != "https://operator.example.test/token" || gotForm.Get("client_id") != "operator-client" || gotForm.Get("scope") != "openid offline_access" || gotForm.Get("refresh_token") != "refresh-old" {
		t.Fatalf("refresh request url=%q form=%v", gotURL, gotForm)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["access_token"] != "access-new" || payload["session_token"] != "access-new" || payload["refresh_token"] != "refresh-new" {
		t.Fatalf("刷新后运行材料未同步：%v", payload)
	}
	if _, exists := payload["oauth_token_endpoint"]; exists {
		t.Fatalf("输入 endpoint 未被清除：%v", payload)
	}
}

func TestConfiguredModeAdapterRegistryAllowsCodexRefreshWithoutScope(t *testing.T) {
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"access_token":"access-new","expires_in":1800}`), nil
	})}
	registry := newDefaultModeAdapterRegistryWithProjectResolver(client, nil, appconfig.VendorOAuthConfigs{
		appconfig.VendorOAuthOpenAICodex: {TokenURL: "https://operator.example.test/token", ClientID: "operator-client"},
	})
	adapter, ok := registry.Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if !ok {
		t.Fatal("Codex CLI OAuth mode adapter missing")
	}
	if _, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 44, Vendor: credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload:  []byte(`{"access_token":"old","session_token":"old","refresh_token":"refresh-old"}`),
	}); err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotForm.Get("client_id") != "operator-client" || gotForm.Get("scope") != "" {
		t.Fatalf("refresh form=%v", gotForm)
	}
}

func TestConfiguredModeAdapterRegistryRefreshesWindsurfThroughExactMode(t *testing.T) {
	var gotURL string
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"access_token":"windsurf-new","refresh_token":"windsurf-refresh-new","expires_in":1800}`), nil
	})}
	registry := newDefaultModeAdapterRegistryWithProjectResolver(client, nil, appconfig.VendorOAuthConfigs{
		appconfig.VendorOAuthWindsurf: {
			TokenURL: "https://windsurf.example.test/token",
			ClientID: "windsurf-client",
			Scope:    "openid offline_access",
		},
	})
	adapter, ok := registry.Lookup(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("缺少 windsurf/oauth 刷新适配器")
	}
	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 45,
		Vendor:            credentialstore.VendorWindsurf,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"access_token":"windsurf-old","refresh_token":"windsurf-refresh-old"}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotURL != "https://windsurf.example.test/token" {
		t.Fatalf("refresh URL=%q", gotURL)
	}
	if gotForm.Get("client_id") != "windsurf-client" || gotForm.Get("scope") != "openid offline_access" || gotForm.Get("refresh_token") != "windsurf-refresh-old" {
		t.Fatalf("refresh form=%v", gotForm)
	}
	if !bytes.Contains(result.Payload, []byte(`"access_token":"windsurf-new"`)) {
		t.Fatalf("刷新结果未保存新 token：%s", result.Payload)
	}
}

func TestModeRegistryInjectsAnthropicTransportIntoExactModes(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(`{"access_token":"claude-new","refresh_token":"claude-refresh-new","expires_in":1800}`), nil
	})}
	registry := newDefaultModeAdapterRegistryWithRuntimeDependencies(nil, nil, nil, client)
	adapter, ok := registry.Lookup(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode)
	if !ok {
		t.Fatal("缺少 anthropic/claude_code 刷新适配器")
	}
	if _, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 46,
		Vendor:            credentialstore.VendorAnthropic,
		AuthMode:          credentialstore.AuthModeClaudeCode,
		Payload:           []byte(`{"access_token":"claude-old","refresh_token":"claude-refresh-old"}`),
	}); err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if !called {
		t.Fatal("Claude 精确模式没有使用注入的出站客户端")
	}
}

// TestGeminiAntigravityRefreshUsesBuiltinProfile 守住重激活路径：canonical 的
// gemini/antigravity mode 必须调用既有 Antigravity 刷新核，并且只把内置公开
// endpoint/client/secret/scope 发给 Google。退回暂停 adapter 或改用 Gemini
// 默认 client 时都会在本测试中变红。
func TestGeminiAntigravityRefreshUsesBuiltinProfile(t *testing.T) {
	var gotURL string
	var gotForm url.Values
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("读取 refresh body 失败：%v", err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("解析 refresh body 失败：%v", err)
		}
		return jsonResponse(`{"access_token":"ag-access-new","refresh_token":"ag-refresh-new","expires_in":1800,"token_type":"Bearer"}`), nil
	})}

	adapter, ok := newDefaultModeAdapterRegistryWithProjectResolver(mockClient, nil, appconfig.VendorOAuthConfigs{
		appconfig.VendorOAuthGemini: {
			TokenURL: "https://generic-gemini.example.test/token",
			ClientID: "generic-gemini-client",
		},
	}).Lookup(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
	if !ok {
		t.Fatal("缺少 gemini/antigravity refresh adapter")
	}
	legacy, ok := adapter.(legacyOAuthModeAdapter)
	if !ok {
		t.Fatalf("adapter type=%T，期望 legacyOAuthModeAdapter 复用刷新契约", adapter)
	}
	wire, ok := legacy.adapter.(adapters.AntigravityRefresh)
	if !ok {
		t.Fatalf("底层 adapter type=%T，期望 adapters.AntigravityRefresh", legacy.adapter)
	}
	approved := providerantigravity.DefaultOAuthConfig()
	if wire.Gemini.Endpoint != providerantigravity.AntigravityOAuthTokenEndpoint || wire.Gemini.ClientID != approved.ClientID {
		t.Fatalf("底层 endpoint/client=(%q,%q)", wire.Gemini.Endpoint, wire.Gemini.ClientID)
	}

	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 94,
		Vendor:            credentialstore.VendorGemini,
		AuthMode:          credentialstore.AuthModeAntigravity,
		Payload: []byte(`{
			"session_token":"ag-session-old",
			"access_token":"ag-access-old",
			"refresh_token":"ag-refresh-old",
			"project_id":"project-preserved",
			"oauth_token_endpoint":"https://attacker.example/token",
			"client_id":"attacker-client",
			"client_secret":"attacker-secret",
			"scope":"attacker-scope"
		}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential 失败：%v", err)
	}
	if gotURL != providerantigravity.AntigravityOAuthTokenEndpoint {
		t.Fatalf("refresh URL=%q，期望 %q", gotURL, providerantigravity.AntigravityOAuthTokenEndpoint)
	}
	wantForm := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "ag-refresh-old",
		"client_id":     approved.ClientID,
		"client_secret": approved.ClientSecret,
	}
	for key, want := range wantForm {
		if got := gotForm.Get(key); got != want {
			t.Errorf("refresh form %s=%q，期望 %q", key, got, want)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("解析刷新结果失败：%v", err)
	}
	if payload["access_token"] != "ag-access-new" || payload["session_token"] != "ag-access-new" {
		t.Fatalf("新 access/session token 未同步：%s", result.Payload)
	}
	if payload["refresh_token"] != "ag-refresh-new" || payload["project_id"] != "project-preserved" {
		t.Fatalf("refresh_token/project_id 未正确合并：%s", result.Payload)
	}
}

func TestDefaultModeAdapterRegistryGeminiOAuthUsesOperatorConfigAndRefreshesSessionToken(t *testing.T) {
	// Gemini 通用 OAuth 继续使用部署者配置，并用新 access_token 替换陈旧 session_token。
	cases := []struct {
		name     string
		vendor   string
		authMode string
	}{
		{name: "gemini", vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeOAuth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operatorEndpoint := "https://operator." + tc.name + ".example.test/oauth/token"
			operatorClientID := "operator-" + tc.name + "-client"
			wantAccessToken := "operator-" + tc.name + "-access"
			wantRefreshToken := "operator-" + tc.name + "-refresh"
			oldSessionToken := "old-" + tc.name + "-session"
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv("HUAKAI_GEMINI_OAUTH_TOKEN_URL", operatorEndpoint)
			t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_ID", operatorClientID)
			t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", "")
			var gotURL string
			var gotForm url.Values
			// operator OAuth 路径生产用 SSRF 防护拨号 client(丢弃自定义 RoundTripper、拨号层校验
			// 目标 IP),无法用 http.DefaultClient mock 驱动。经 newDefaultModeAdapterRegistry 注入
			// mock client 直驱刷新逻辑;SSRF 防护本身另由 TestGeminiRefreshHTTPClientIsSSRFProtectedAtWiring 覆盖。
			mockClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotURL = r.URL.String()
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read refresh body: %v", err)
				}
				gotForm, err = url.ParseQuery(string(body))
				if err != nil {
					t.Fatalf("parse refresh body %q: %v", string(body), err)
				}
				return jsonResponse(`{"access_token":"` + wantAccessToken + `","refresh_token":"` + wantRefreshToken + `","expires_in":1800,"token_type":"Bearer"}`), nil
			})}

			adapter, ok := newDefaultModeAdapterRegistryWithProjectResolver(mockClient, nil, nil).Lookup(tc.vendor, tc.authMode)
			if !ok {
				t.Fatalf("missing mode refresh adapter %s/%s", tc.vendor, tc.authMode)
			}
			result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
				ProviderAccountID: 91,
				Vendor:            tc.vendor,
				AuthMode:          tc.authMode,
				Payload: []byte(`{
					"session_token":"` + oldSessionToken + `",
					"access_token":"old-access-token",
					"refresh_token":"refresh-from-credential",
					"oauth_token_endpoint":"https://attacker.example.test/token",
					"token_endpoint":"https://attacker.example.test/alt-token",
					"client_id":"credential-client",
					"client_secret":"credential-secret"
				}`),
			})
			if err != nil {
				t.Fatalf("RefreshCredential: %v", err)
			}
			if gotURL != operatorEndpoint {
				t.Fatalf("refresh endpoint=%q want operator endpoint %q", gotURL, operatorEndpoint)
			}
			if gotForm.Get("client_id") != operatorClientID {
				t.Fatalf("client_id=%q want operator client %q", gotForm.Get("client_id"), operatorClientID)
			}
			if got := gotForm.Get("client_secret"); got != "" {
				t.Fatalf("client_secret=%q want empty when operator secret env is empty", got)
			}
			var payload map[string]string
			if err := json.Unmarshal(result.Payload, &payload); err != nil {
				t.Fatalf("result payload json: %v", err)
			}
			if got := payload["access_token"]; got != wantAccessToken {
				t.Fatalf("access_token=%q want refreshed token %q; payload=%s", got, wantAccessToken, result.Payload)
			}
			if got := payload["session_token"]; got != wantAccessToken {
				t.Fatalf("session_token=%q want refreshed access token %q; old token %q must not survive; payload=%s", got, wantAccessToken, oldSessionToken, result.Payload)
			}
			if got := payload["refresh_token"]; got != wantRefreshToken {
				t.Fatalf("refresh_token=%q want rotated token %q; payload=%s", got, wantRefreshToken, result.Payload)
			}
		})
	}
}

type modeProjectResolverStub struct {
	projectID string
	calls     int
	token     string
}

func (s *modeProjectResolverStub) ResolveProjectID(_ context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	return s.projectID, nil
}

func TestDefaultModeAdapterRegistryWiresAntigravityProjectResolver(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_GEMINI_OAUTH_TOKEN_URL", "https://operator.antigravity.example.test/oauth/token")
	t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_ID", "operator-antigravity-client")
	t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", "")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"access_token":"access-after-refresh","refresh_token":"refresh-after-refresh","expires_in":1800}`), nil
	})}
	resolver := &modeProjectResolverStub{projectID: "project-through-registry"}
	registry := newDefaultModeAdapterRegistryWithProjectResolver(client, resolver, nil)
	adapter, ok := registry.Lookup(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("缺少 antigravity/oauth refresh adapter")
	}
	result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 95,
		Vendor:            credentialstore.VendorAntigravity,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"access_token":"access-old","refresh_token":"refresh-old"}`),
	})
	if err != nil {
		t.Fatalf("RefreshCredential 失败：%v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("解析刷新结果失败：%v", err)
	}
	if resolver.calls != 1 || resolver.token != "access-after-refresh" {
		t.Fatalf("resolver 接线未生效：calls=%d token=%q", resolver.calls, resolver.token)
	}
	if payload["project_id"] != "project-through-registry" || payload["project_metadata_status"] != "resolved" {
		t.Fatalf("project 未经 registry 接线写回：%s", result.Payload)
	}
}

func TestWindsurfManualModeAdapterRejectsRefreshTokenOnlyCredential(t *testing.T) {
	// 修掉的回归:一个只含 refresh_token 的已存储 Windsurf OAuth payload 无法被运行时
	// session adapter 使用,因此定时 refresh 必须 fail closed,而不是悄悄把它当作一次
	// 手动 no-op。Mutation 自检:删掉 session/access-token 守卫会返回
	// ErrNoRefreshRequired,使本测试转红。
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("missing windsurf/oauth mode adapter")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 92,
		Vendor:            credentialstore.VendorWindsurf,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"refresh_token":"refresh-only"}`),
	})
	if err == nil {
		t.Fatal("RefreshCredential err=nil, want invalid credential material")
	}
	if errors.Is(err, ErrNoRefreshRequired) {
		t.Fatalf("RefreshCredential err=%v, want fail-closed invalid credential material", err)
	}
	if !errors.Is(err, adapters.ErrInvalidCredentialMaterial) {
		t.Fatalf("RefreshCredential err=%v, want ErrInvalidCredentialMaterial", err)
	}
}

func TestWindsurfManualModeAdapterPreservesSessionTokenManualNoop(t *testing.T) {
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("missing windsurf/oauth mode adapter")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 93,
		Vendor:            credentialstore.VendorWindsurf,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"session_token":"windsurf-session-token"}`),
	})
	if !errors.Is(err, ErrNoRefreshRequired) {
		t.Fatalf("RefreshCredential err=%v, want ErrNoRefreshRequired for manual session token", err)
	}
}

func TestModeRefreshCodexOperatorConfigFailureRecordsOperatorClass(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 45, TenantID: 1, ProviderAccountID: 102,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			CredentialVersion: 3, PlaintextPayload: []byte(`{"refresh_token":"rt-old","client_id_source":"operator_config"}`),
		},
	}
	refresher := &AccountCredentialRefresher{store: store, registry: DefaultModeAdapterRegistry(), now: func() time.Time {
		return time.Date(2026, 5, 24, 14, 20, 0, 0, time.UTC)
	}}

	err := refresher.Refresh(context.Background(), 102)
	if !errors.Is(err, adapters.ErrCodexOAuthConfigRequired) {
		t.Fatalf("Refresh err=%v, want ErrCodexOAuthConfigRequired", err)
	}
	want := []string{"probe", "tx_begin", "failure:45:operator_config_required"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func TestMockTokenExchangeAdapterRefreshesAzureWithoutSDK(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s want POST", r.Method)
		}
		return jsonResponse(`{"access_token":"azure-access","expires_in":1800,"token_type":"Bearer"}`), nil
	})}

	raw := []byte(`{"mock_token_endpoint":"http://mock.local/token","tenant_id":"t"}`)
	result, err := (mockTokenExchangeAdapter{providerName: "azure", client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "azure-access" || got["token_type"] != "Bearer" {
		t.Fatalf("payload=%v", got)
	}
	if time.Until(result.AccessExpiresAt) <= time.Minute {
		t.Fatalf("AccessExpiresAt not advanced: %s", result.AccessExpiresAt)
	}
}

func TestMetadataTokenAdapterUsesStdlibMetadataRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Metadata-Flavor"); got != "Google" {
			t.Fatalf("Metadata-Flavor=%q want Google", got)
		}
		return jsonResponse(`{"access_token":"gcp-access","expires_in":3600}`), nil
	})}

	raw := []byte(`{"metadata_token_endpoint":"http://metadata.local/token","client_email":"svc@example.test"}`)
	result, err := (metadataTokenAdapter{client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "gcp-access" {
		t.Fatalf("payload=%v", got)
	}
}

// assertSSRFBlocksLoopbackEndpoint 在不注入 client(强制走生产 fallback)的情况下,
// 用一个 credential 驱动的 token adapter 去打一个真实的 loopback HTTP 服务器——若被
// 打到,该服务器会回交一个可用的 access_token。受 SSRF 保护的 fallback 必须拒绝拨号
// 127.0.0.1,因此 run() 必须返回一个拨号错误且绝不捕获到 token。这是 mock 与
// metadata adapter 共享的区分性 fixture。
//
// Mutation check:在任一 adapter 中还原裸 `http.DefaultClient` fallback——请求随后会
// 打到 loopback 服务器,RefreshCredential 成功并捕获到 token,err 为 nil,本断言转红。
// 这条成功路径证明该回归是真实的 token 外泄,而非仅仅一个表面错误。
func assertSSRFBlocksLoopbackEndpoint(t *testing.T, run func(endpoint string) error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"ssrf-leaked-token","expires_in":3600}`))
	}))
	defer srv.Close()

	if err := run(srv.URL); err == nil {
		t.Fatal("SSRF guard must reject the loopback token endpoint; got nil error (token was captured)")
	}
}

func TestMockTokenExchangeAdapterFallbackBlocksInternalEndpoint(t *testing.T) {
	assertSSRFBlocksLoopbackEndpoint(t, func(endpoint string) error {
		raw := []byte(`{"mock_token_endpoint":"` + endpoint + `"}`)
		_, err := (mockTokenExchangeAdapter{providerName: "azure"}).RefreshCredential(context.Background(), ModeRefreshInput{
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Payload: raw, Now: time.Now(),
		})
		return err
	})
}

func TestMetadataTokenAdapterFallbackBlocksInternalEndpoint(t *testing.T) {
	assertSSRFBlocksLoopbackEndpoint(t, func(endpoint string) error {
		raw := []byte(`{"metadata_token_endpoint":"` + endpoint + `","client_email":"svc@example.test"}`)
		_, err := (metadataTokenAdapter{}).RefreshCredential(context.Background(), ModeRefreshInput{
			Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Payload: raw, Now: time.Now(),
		})
		return err
	})
}

func TestRefreshRemoteExchangePrecedesShortPersistenceTransaction(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 44, TenantID: 1, ProviderAccountID: 101,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken,
			CredentialVersion: 3, PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, recordingModeAdapter{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	refresher := &AccountCredentialRefresher{store: store, registry: registry, now: func() time.Time {
		return time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	}}
	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := []string{"probe", "adapter:44", "tx_begin", "save:44"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func TestGeminiFallbackAuditWrittenInRefreshTransaction(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 55, TenantID: 1, ProviderAccountID: 101,
			Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist,
			CredentialVersion: 2, PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, recordingModeAdapter{
		calls:   &calls,
		payload: []byte(`{"refresh_token":"rt-new","cross_client_fallback_attempted":true,"cross_client_fallback_from":"code_assist","cross_client_fallback_to":"ai_studio"}`),
	}); err != nil {
		t.Fatal(err)
	}
	refresher := &AccountCredentialRefresher{store: store, registry: registry, now: func() time.Time {
		return time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	}}
	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := []string{
		"probe", "adapter:55", "tx_begin",
		"audit:gemini_cross_client_fallback:code_assist:ai_studio:true", "save:55",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func splitCredentialModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

// TestRefreshLockedRecordSurfacesSaveFailureError 守卫:当持久化 refresh-failure
// 状态本身失败时,refreshLockedRecord 必须把该错误暴露出来(与起因 join),而不是
// 用 `_ =` 丢弃。否则凭据的失败状态(冷却 / 重试计数 / 原因)会被悄悄丢失,scheduler
// 会按陈旧状态反复重试。
//
// Mutation check:还原 `_ = txStore.SaveRefreshFailure(...)`,返回的错误就不再 wrap
// 持久化 sentinel → 转红。使用 adapter-missing 分支(无需活的 adapter)。
func TestRefreshLockedRecordSurfacesSaveFailureError(t *testing.T) {
	saveErr := errors.New("save refresh failure write failed")
	calls := []string{}
	tx := &recordingRefreshTx{calls: &calls, saveFailureErr: saveErr}
	refresher := &AccountCredentialRefresher{registry: DefaultModeAdapterRegistry(), now: func() time.Time { return time.Unix(0, 0).UTC() }}
	rec := credentialstore.CredentialRecord{ID: 7, Vendor: "nonexistent-vendor", AuthMode: "oauth"}

	err := refresher.refreshLockedRecord(context.Background(), tx, 7, rec)
	if !errors.Is(err, saveErr) {
		t.Fatalf("refreshLockedRecord must surface the SaveRefreshFailure persistence error; got %v", err)
	}
	if !errors.Is(err, ErrProviderAdapterMissing) {
		t.Fatalf("should also preserve the adapter-missing cause; got %v", err)
	}
}

func TestRefreshLockedRecordPropagatesFailureClassToSchedulerLog(t *testing.T) {
	registry := NewModeAdapterRegistry()
	if err := registry.Register("testvendor", "oauth", failingModeAdapter{err: errors.New("oauth token endpoint returned status 401: invalid_grant")}); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	tx := &recordingRefreshTx{calls: &calls}
	refresher := &AccountCredentialRefresher{registry: registry, now: func() time.Time { return time.Unix(0, 0).UTC() }}
	rec := credentialstore.CredentialRecord{ID: 8, Vendor: "testvendor", AuthMode: "oauth"}

	err := refresher.refreshLockedRecord(context.Background(), tx, 8, rec)
	if got := auth.RefreshAuditOutcomeFromError(err); got != string(auth.OutcomeAuthExpired) {
		t.Fatalf("日志结果=%q，期望 auth_expired；err=%v", got, err)
	}
	if got := strings.Join(calls, ","); !strings.Contains(got, "failure:8:invalid_grant") {
		t.Fatalf("刷新失败状态未按同一分类落库：%s", got)
	}
}

type recordingRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
}

func (s *recordingRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingRefreshStore) WithRefreshTransaction(_ context.Context, fn func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingRefreshTx{calls: s.calls, rec: s.rec}
	return fn(tx, tx)
}

type recordingRefreshTx struct {
	calls          *[]string
	rec            credentialstore.CredentialRecord
	saveFailureErr error // 注入:让 SaveRefreshFailure 返回错误(测试)
}

func (tx *recordingRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	// 锁键现以单个 text 参数传入(修复 pgx int64→text 编码 bug),按 string 记录。
	*tx.calls = append(*tx.calls, "lock:"+args[0].(string))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "save:"+strconv.FormatInt(rec.ID, 10))
	return nil
}

func (tx *recordingRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+strconv.FormatInt(rec.ID, 10)+":"+failureClass)
	return tx.saveFailureErr
}

func (tx *recordingRefreshTx) SetNextAttemptThrottle(_ context.Context, rec credentialstore.CredentialRecord, _ time.Time) error {
	*tx.calls = append(*tx.calls, "throttle:"+strconv.FormatInt(rec.ID, 10))
	return nil
}

func (tx *recordingRefreshTx) InsertAuditEvent(_ context.Context, e credentialstore.AuditEvent) error {
	*tx.calls = append(*tx.calls, "audit:"+e.EventType+":"+
		auditPayloadString(e.Payload, "from_client")+":"+
		auditPayloadString(e.Payload, "to_client")+":"+
		strconv.FormatBool(auditPayloadBool(e.Payload, "success")))
	return nil
}

func auditPayloadString(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

func auditPayloadBool(payload map[string]any, key string) bool {
	if v, ok := payload[key].(bool); ok {
		return v
	}
	return false
}

type recordingModeAdapter struct {
	calls   *[]string
	payload []byte
}

type failingModeAdapter struct {
	err error
}

func (a failingModeAdapter) RefreshCredential(context.Context, ModeRefreshInput) (ModeRefreshResult, error) {
	return ModeRefreshResult{}, a.err
}

func (a recordingModeAdapter) RefreshCredential(_ context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	*a.calls = append(*a.calls, "adapter:"+strconv.FormatInt(in.CredentialID, 10))
	payload := a.payload
	if len(payload) == 0 {
		payload = []byte(`{"refresh_token":"rt-new"}`)
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestDefaultModeAdapterRegistryRoutesUpstreamOAuthRefreshModes(t *testing.T) {
	// Mutation:删掉 grok/xai_oauth 或 kimi/kimi_oauth 任一的 register(...),
	// 或把 tokenURL/clientID 指向错误的值;本测试就会转红。
	registry := DefaultModeAdapterRegistry()
	cases := []struct {
		vendor, authMode, wantTokenURL, wantClientID string
	}{
		{credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, "https://auth.x.ai/oauth2/token", "b1a00492-073a-47ea-816f-4c329264a828"},
		{credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth, "https://auth.kimi.com/api/oauth/token", "17e5f671-d194-4dfb-9706-5516cb48c098"},
	}
	for _, tc := range cases {
		adapter, ok := registry.Lookup(tc.vendor, tc.authMode)
		if !ok {
			t.Fatalf("missing mode refresh adapter %s/%s", tc.vendor, tc.authMode)
		}
		got, ok := adapter.(builtinRefreshTokenModeAdapter)
		if !ok {
			t.Fatalf("%s/%s adapter type=%T want builtinRefreshTokenModeAdapter", tc.vendor, tc.authMode, adapter)
		}
		if got.tokenURL != tc.wantTokenURL {
			t.Fatalf("%s/%s tokenURL=%q want %q", tc.vendor, tc.authMode, got.tokenURL, tc.wantTokenURL)
		}
		if got.clientID != tc.wantClientID {
			t.Fatalf("%s/%s clientID=%q want %q", tc.vendor, tc.authMode, got.clientID, tc.wantClientID)
		}
	}
}

func TestBuiltinRefreshTokenModeAdapterRotatesTokens(t *testing.T) {
	var gotGrant, gotClient, gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotClient = r.Form.Get("client_id")
		gotRefresh = r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	adapter := builtinRefreshTokenModeAdapter{providerName: "grok", tokenURL: srv.URL, clientID: "client-xyz", client: srv.Client()}
	res, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{Payload: []byte(`{"access_token":"old","refresh_token":"old-refresh"}`)})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(res.Payload, &fields); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	// Mutation:删掉 executeTokenRequest 中的 refresh_token 轮换 -> 转红。
	if fields["access_token"] != "new-access" {
		t.Fatalf("access_token=%v want new-access", fields["access_token"])
	}
	if fields["refresh_token"] != "new-refresh" {
		t.Fatalf("refresh_token=%v want new-refresh (rotation)", fields["refresh_token"])
	}
	if gotGrant != "refresh_token" || gotClient != "client-xyz" || gotRefresh != "old-refresh" {
		t.Fatalf("token request grant=%q client=%q refresh=%q", gotGrant, gotClient, gotRefresh)
	}
}

func TestBuiltinRefreshTokenModeAdapterNoRefreshTokenSkips(t *testing.T) {
	// Mutation:把缺失 refresh_token 当作错误而非 ErrNoRefreshRequired -> 转红。
	adapter := builtinRefreshTokenModeAdapter{providerName: "kimi", tokenURL: "https://auth.kimi.com/api/oauth/token", clientID: "x"}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{Payload: []byte(`{"access_token":"only"}`)})
	if !errors.Is(err, ErrNoRefreshRequired) {
		t.Fatalf("err=%v want ErrNoRefreshRequired when refresh_token absent", err)
	}
}
