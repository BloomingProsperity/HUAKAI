package crssource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestFetch归一六类账号并移除来源端点覆盖(t *testing.T) {
	var loginCalls, exportCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/auth/login":
			loginCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"token":"admin-secret"}`))
		case "/admin/sync/export-accounts":
			exportCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer admin-secret" || r.URL.Query().Get("include_secrets") != "true" {
				t.Fatalf("导出请求鉴权或查询参数不正确")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "success": true,
  "data": {
    "claudeAccounts": [{"id":"ca-1","name":"Claude","authType":"setup-token","isActive":true,"schedulable":true,"credentials":{"access_token":"setup-secret","base_url":"https://attacker.invalid/v1"}}],
    "claudeConsoleAccounts": [{"id":"cc-1","name":"Console","isActive":true,"schedulable":true,"maxConcurrentTasks":8,"credentials":{"api_key":"ak"}}],
    "openaiOAuthAccounts": [{"id":"oa-1","name":"OpenAI OAuth","isActive":true,"schedulable":true,"credentials":{"access_token":"oa","refresh_token":"or"}}],
    "openaiResponsesAccounts": [{"id":"or-1","name":"Responses","isActive":true,"schedulable":true,"credentials":{"api_key":"rk"}}],
    "geminiOAuthAccounts": [{"id":"go-1","name":"Gemini OAuth","isActive":true,"schedulable":true,"credentials":{"refresh_token":"gr"}}],
    "geminiApiKeyAccounts": [{"id":"ga-1","name":"Gemini Key","isActive":true,"schedulable":true,"credentials":{"api_key":"gk"}}]
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.Split(host, ":")[0]
	client := New(server.Client(), Policy{AllowedHosts: []string{host}, AllowPrivateHosts: true, AllowInsecureHTTP: true})
	client.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	result, err := client.Fetch(context.Background(), Input{BaseURL: server.URL, Username: "operator", Password: "password"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if loginCalls.Load() != 1 || exportCalls.Load() != 1 || len(result.Accounts) != 6 {
		t.Fatalf("calls login=%d export=%d accounts=%d", loginCalls.Load(), exportCalls.Load(), len(result.Accounts))
	}
	setup := result.Accounts[0]
	if setup.AuthMode != "claude_setup_token" || setup.Credentials["setup_token"] != "setup-secret" || setup.Credentials["access_token"] != nil {
		t.Fatalf("Setup Token 归一错误：%+v", setup)
	}
	if _, exists := setup.Credentials["base_url"]; exists || len(setup.Warnings) != 1 || setup.Warnings[0] != "source_endpoint_override_removed" {
		t.Fatalf("来源端点覆盖未移除：%+v", setup)
	}
	if result.Accounts[1].Concurrency != 8 || result.Accounts[4].AuthMode != "code_assist" || result.SourceRef() == "" {
		t.Fatalf("账号映射错误：%+v source_ref=%q", result.Accounts, result.SourceRef())
	}
}

func TestSourceModeAllowed绑定生产归一化矩阵(t *testing.T) {
	tests := []struct {
		sourceType string
		vendor     string
		authMode   string
		want       bool
	}{
		{"claude", credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, true},
		{"claude", credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, true},
		{"claude_console", credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, true},
		{"openai_oauth", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, true},
		{"openai_responses", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, true},
		{"gemini_oauth", credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, true},
		{"gemini_api_key", credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, true},
		{"openai_oauth", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, false},
		{"gemini_oauth", credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne, false},
		{"unknown", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, false},
	}
	for _, test := range tests {
		if got := SourceModeAllowed(test.sourceType, test.vendor, test.authMode); got != test.want {
			t.Fatalf("SourceModeAllowed(%s,%s,%s)=%v，期望 %v",
				test.sourceType, test.vendor, test.authMode, got, test.want)
		}
	}
}

func TestFetch白名单和二次解析均为FailClosed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/web/auth/login" {
			_, _ = w.Write([]byte(`{"success":true,"token":"token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()
	host := strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[0]

	client := New(server.Client(), Policy{AllowedHosts: []string{"allowed.invalid"}, AllowPrivateHosts: true, AllowInsecureHTTP: true})
	client.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := client.Fetch(context.Background(), Input{BaseURL: server.URL, Username: "u", Password: "p"}); !errors.Is(err, ErrEndpointDenied) || calls.Load() != 0 {
		t.Fatalf("非白名单端点 err=%v calls=%d", err, calls.Load())
	}

	client = New(server.Client(), Policy{AllowedHosts: []string{host}, AllowInsecureHTTP: true})
	var lookups atomic.Int32
	client.lookup = func(context.Context, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := client.Fetch(context.Background(), Input{BaseURL: server.URL, Username: "u", Password: "p"}); !errors.Is(err, ErrEndpointDenied) || calls.Load() != 1 {
		t.Fatalf("二次解析漂移 err=%v calls=%d lookups=%d", err, calls.Load(), lookups.Load())
	}
}

func TestResolveAllowedHost拒绝混合解析并去重(t *testing.T) {
	lookup := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("203.0.113.10"),
			netip.MustParseAddr("203.0.113.10"),
			netip.MustParseAddr("127.0.0.1"),
		}, nil
	}
	if _, err := resolveAllowedHost(context.Background(), Policy{AllowedHosts: []string{"crs.example"}}, lookup, "crs.example"); !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("混合公私网解析必须拒绝，实际 err=%v", err)
	}

	addresses, err := resolveAllowedHost(context.Background(), Policy{
		AllowedHosts: []string{"crs.example"}, AllowPrivateHosts: true,
	}, lookup, "crs.example")
	if err != nil {
		t.Fatalf("允许私网解析: %v", err)
	}
	if len(addresses) != 2 {
		t.Fatalf("地址应去重，实际 %+v", addresses)
	}
	if _, err := resolveAllowedHost(context.Background(), Policy{AllowedHosts: []string{"other.example"}}, lookup, "crs.example"); !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("非白名单域名必须拒绝，实际 err=%v", err)
	}
}

func TestFetch拒绝超量和异常响应且不泄漏上游正文(t *testing.T) {
	t.Run("账号超量", func(t *testing.T) {
		rows := strings.Repeat(`{"id":"x","credentials":{"api_key":"k"}},`, maxExportAccounts) + `{"id":"last","credentials":{"api_key":"k"}}`
		client, closeServer := sourceTestClient(t, `{"success":true,"data":{"geminiApiKeyAccounts":[`+rows+`]}}`)
		defer closeServer()
		_, err := client.Fetch(context.Background(), Input{BaseURL: clientBaseURL(client), Username: "u", Password: "p"})
		if !errors.Is(err, ErrTooManyAccounts) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("登录错误不回传正文", func(t *testing.T) {
		secretBody := "database-password-sentinel"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(secretBody))
		}))
		defer server.Close()
		host := strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[0]
		client := New(server.Client(), Policy{AllowedHosts: []string{host}, AllowPrivateHosts: true, AllowInsecureHTTP: true})
		client.lookup = func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		_, err := client.Fetch(context.Background(), Input{BaseURL: server.URL, Username: "u", Password: "p"})
		if !errors.Is(err, ErrAuthentication) || strings.Contains(err.Error(), secretBody) {
			t.Fatalf("err=%v", err)
		}
	})
}

type testClientWithURL struct {
	*Client
	baseURL string
}

func sourceTestClient(t *testing.T, exportBody string) (*testClientWithURL, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/web/auth/login" {
			_, _ = w.Write([]byte(`{"success":true,"token":"token"}`))
			return
		}
		_, _ = w.Write([]byte(exportBody))
	}))
	host := strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[0]
	client := New(server.Client(), Policy{AllowedHosts: []string{host}, AllowPrivateHosts: true, AllowInsecureHTTP: true})
	client.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	return &testClientWithURL{Client: client, baseURL: server.URL}, server.Close
}

func clientBaseURL(client *testClientWithURL) string { return client.baseURL }
