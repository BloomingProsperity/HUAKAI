package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func TestChatGPTRefreshIgnoresHostileOAuthTokenEndpoint(t *testing.T) {
	// 判别 mutation：把 ChatGPT refresh endpoint 选择改回读取 credential oauth_token_endpoint 时，请求会打到 attacker.test。
	var capturedHost string
	client := &http.Client{Transport: chatGPTSSRFRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHost = r.URL.Host
		return chatGPTSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"oauth_token_endpoint":"http://attacker.test/oauth/token"
	}`)

	_, _, err := (adapters.ChatGPTRefresh{HTTPClient: client}).RefreshForProvider(context.Background(), 401, "openai", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if capturedHost != "auth.openai.com" {
		t.Fatalf("token endpoint host=%q, want built-in auth.openai.com", capturedHost)
	}
}

func TestChatGPTRefreshHostileCredScrubedAfterRefresh(t *testing.T) {
	// 判别 mutation：删除 mergeTokenResponse hostile 字段 scrub 时，写回 payload 会残留 attacker 字段。
	client := &http.Client{Transport: chatGPTSSRFRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return chatGPTSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"oauth_token_endpoint":"http://attacker.test/oauth/token",
		"client_secret":"leaked-from-cred",
		"fallback_client_id":"attacker-cid",
		"setup_token":"attacker-setup",
		"long_lived_setup_token":"attacker-long-lived",
		"keep":"yes"
	}`)

	raw, _, err := (adapters.ChatGPTRefresh{HTTPClient: client}).RefreshForProvider(context.Background(), 402, "openai", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("unmarshal merged payload: %v", err)
	}
	for _, key := range []string{"oauth_token_endpoint", "client_secret", "fallback_client_id", "setup_token", "long_lived_setup_token"} {
		if _, ok := merged[key]; ok {
			t.Fatalf("hostile key %q survived ChatGPT refresh payload: %s", key, raw)
		}
	}
	if merged["keep"] != "yes" {
		t.Fatalf("benign credential field keep=%v, want yes", merged["keep"])
	}
}

func TestChatGPTRefreshSyncsSessionTokenFromAccessToken(t *testing.T) {
	// 判别 mutation：删除 ChatGPT refresh 的 session_token 同步时，运行时会继续拿旧 token。
	client := &http.Client{Transport: chatGPTSSRFRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return chatGPTSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "AT_NEW", "refresh_token": "RT_NEW", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"AT_OLD",
		"session_token":"AT_OLD",
		"refresh_token":"RT_OLD"
	}`)

	raw, _, err := (adapters.ChatGPTRefresh{HTTPClient: client}).RefreshForProvider(context.Background(), 404, "openai", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("unmarshal merged payload: %v", err)
	}
	if got := merged["access_token"]; got != "AT_NEW" {
		t.Fatalf("access_token=%v want AT_NEW; payload=%s", got, raw)
	}
	if got := merged["session_token"]; got != "AT_NEW" {
		t.Fatalf("session_token=%v want AT_NEW; old token must not survive; payload=%s", got, raw)
	}
}

func TestChatGPTRefreshHTTPClientIsSSRFProtectedAtWiring(t *testing.T) {
	// 判别 mutation：把 ChatGPT mode wiring 改回 legacy OpenAIRefresh/http.DefaultClient 时，本测试不会拿到 ErrOAuthEndpointBlocked。
	var lookupHost string
	restoreLookup := auth.SwapOAuthIPLookupForTesting(func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookupHost = host
		return []net.IPAddr{{IP: net.ParseIP("192.168.1.1")}}, nil
	})
	t.Cleanup(restoreLookup)

	adapter, ok := credentialworker.DefaultModeAdapterRegistry().Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth)
	if !ok {
		t.Fatal("missing OpenAI ChatGPT OAuth refresh adapter")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	_, err := adapter.RefreshCredential(ctx, credentialworker.ModeRefreshInput{
		ProviderAccountID: 403,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeChatGPTOAuth,
		Payload: []byte(`{
			"access_token":"old",
			"refresh_token":"rt-old"
		}`),
		Now: time.Now().UTC(),
	})
	if !errors.Is(err, auth.ErrOAuthEndpointBlocked) {
		t.Fatalf("RefreshCredential err=%v, want ErrOAuthEndpointBlocked from SSRF-protected wiring", err)
	}
	if lookupHost != "auth.openai.com" {
		t.Fatalf("SSRF lookup host=%q, want auth.openai.com", lookupHost)
	}
}

type chatGPTSSRFRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatGPTSSRFRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func chatGPTSSRFJSONResponse(status int, body map[string]any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}
