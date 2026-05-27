package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func TestGeminiRefreshIgnoresHostileOAuthTokenEndpoint(t *testing.T) {
	// 判别 mutation：把 endpoint 选择改回读取 credential oauth_token_endpoint 时，请求会打到 attacker.test。
	var capturedHost string
	client := &http.Client{Transport: geminiSSRFRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHost = r.URL.Host
		return geminiSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"oauth_token_endpoint":"http://attacker.test/oauth/token"
	}`)

	_, _, err := (adapters.GeminiRefresh{
		ClientID:            credentialacq.GeminiPublicCLIClientID,
		ClientSecret:        "operator-secret",
		HTTPClient:          client,
		RequireClientSecret: true,
	}).RefreshForProvider(context.Background(), 301, "gemini", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if capturedHost != "oauth2.googleapis.com" {
		t.Fatalf("token endpoint host=%q, want built-in oauth2.googleapis.com", capturedHost)
	}
}

func TestGeminiRefreshIgnoresHostileClientSecret(t *testing.T) {
	// 判别 mutation：operator secret 为空时回退 credential client_secret 已由 fail-closed 测试覆盖；这里守 operator 值优先。
	var captured url.Values
	client := &http.Client{Transport: geminiSSRFRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = geminiSSRFForm(t, r)
		return geminiSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"client_secret":"leaked-from-cred"
	}`)

	_, _, err := (adapters.GeminiRefresh{
		Endpoint:            credentialacq.DefaultGeminiTokenEndpoint,
		ClientID:            credentialacq.GeminiPublicCLIClientID,
		ClientSecret:        "operator-secret",
		HTTPClient:          client,
		RequireClientSecret: true,
	}).RefreshForProvider(context.Background(), 302, "gemini", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if got := captured.Get("client_secret"); got != "operator-secret" {
		t.Fatalf("client_secret=%q, want operator-secret", got)
	}
	if got := captured.Get("client_secret"); got == "leaked-from-cred" {
		t.Fatalf("credential client_secret leaked into outbound form")
	}
}

func TestGeminiRefreshFallbackUsesBuiltinClientIDNotCredField(t *testing.T) {
	// 判别 mutation：显式打开 fallback 的 unit 场景若改回读取 credential fallback_client_id，
	// 第二次请求会发 attacker-cid；生产 wiring 当前显式关闭，不依赖本测试启用。
	var attempts int
	var fallbackForm url.Values
	client := &http.Client{Transport: geminiSSRFRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return geminiSSRFJSONResponse(http.StatusBadRequest, map[string]any{"error": "invalid_grant"}), nil
		}
		fallbackForm = geminiSSRFForm(t, r)
		return geminiSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "fallback-access", "refresh_token": "fallback-refresh", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"client_family":"code_assist",
		"fallback_client_family":"google_one",
		"fallback_client_id":"attacker-cid"
	}`)

	_, _, err := (adapters.GeminiRefresh{
		Endpoint:                 credentialacq.DefaultGeminiTokenEndpoint,
		ClientID:                 "operator-primary-cid",
		ClientSecret:             "operator-secret",
		HTTPClient:               client,
		AllowCrossClientFallback: true,
		SourceClientFamily:       "code_assist",
		RequireClientSecret:      true,
	}).RefreshForProvider(context.Background(), 303, "gemini", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("token endpoint attempts=%d, want primary plus fallback", attempts)
	}
	if got := fallbackForm.Get("client_id"); got != credentialacq.GeminiPublicCLIClientID {
		t.Fatalf("fallback client_id=%q, want built-in %q", got, credentialacq.GeminiPublicCLIClientID)
	}
	if got := fallbackForm.Get("client_id"); got == "attacker-cid" {
		t.Fatalf("credential fallback_client_id leaked into outbound form")
	}
}

func TestGeminiRefreshFallbackExplicitlyDisabledForGoogleCLI(t *testing.T) {
	// 判别 mutation：把 newGeminiBuiltinClientOAuthModeAdapter 改回
	// AllowCrossClientFallback=true 时，本测试会在 Code Assist/Google One wiring 变红。
	cases := []struct {
		name     string
		authMode string
	}{
		{name: "code_assist", authMode: credentialstore.AuthModeCodeAssist},
		{name: "google_one", authMode: credentialstore.AuthModeGoogleOne},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, ok := credentialworker.DefaultModeAdapterRegistry().Lookup(credentialstore.VendorGemini, tc.authMode)
			if !ok {
				t.Fatalf("missing Gemini %s refresh adapter", tc.authMode)
			}
			geminiValue, ok := findGeminiRefreshValue(reflect.ValueOf(adapter))
			if !ok {
				t.Fatalf("adapter %T does not contain adapters.GeminiRefresh", adapter)
			}
			fallback := geminiValue.FieldByName("AllowCrossClientFallback")
			if !fallback.IsValid() || fallback.Kind() != reflect.Bool {
				t.Fatalf("GeminiRefresh.AllowCrossClientFallback field missing or non-bool")
			}
			if fallback.Bool() {
				t.Fatalf("Gemini %s wiring enables cross-client fallback; Google public CLI ClientID is shared, so this must stay explicit-off", tc.authMode)
			}
		})
	}
}

func TestGeminiRefreshClientSecretFromCredRejectedWhenOperatorNotConfigured(t *testing.T) {
	// 判别 mutation：缺 operator secret 时若回退 credential client_secret，本测试会发生 HTTP 调用。
	var calls int
	client := &http.Client{Transport: geminiSSRFRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return geminiSSRFJSONResponse(http.StatusOK, map[string]any{
			"access_token": "unexpected", "refresh_token": "unexpected-rt", "expires_in": 3600,
		}), nil
	})}
	cred := []byte(`{
		"access_token":"old",
		"refresh_token":"rt-old",
		"client_secret":"leaked-from-cred"
	}`)

	_, _, err := (adapters.GeminiRefresh{
		Endpoint:            credentialacq.DefaultGeminiTokenEndpoint,
		ClientID:            credentialacq.GeminiPublicCLIClientID,
		HTTPClient:          client,
		RequireClientSecret: true,
	}).RefreshForProvider(context.Background(), 304, "gemini", cred)
	if !errors.Is(err, adapters.ErrGeminiOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrGeminiOAuthConfigRequired", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls=%d, want 0 when operator client_secret is missing", calls)
	}
}

func TestGeminiRefreshHostileCredScrubedAfterRefresh(t *testing.T) {
	// 判别 mutation：删除 mergeTokenResponse hostile 字段 scrub 时，写回 payload 会残留 attacker 字段。
	client := &http.Client{Transport: geminiSSRFRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return geminiSSRFJSONResponse(http.StatusOK, map[string]any{
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

	raw, _, err := (adapters.GeminiRefresh{
		Endpoint:            credentialacq.DefaultGeminiTokenEndpoint,
		ClientID:            credentialacq.GeminiPublicCLIClientID,
		ClientSecret:        "operator-secret",
		HTTPClient:          client,
		RequireClientSecret: true,
	}).RefreshForProvider(context.Background(), 305, "gemini", cred)
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("unmarshal merged payload: %v", err)
	}
	for _, key := range []string{"oauth_token_endpoint", "client_secret", "fallback_client_id", "setup_token", "long_lived_setup_token"} {
		if _, ok := merged[key]; ok {
			t.Fatalf("hostile key %q survived Gemini refresh payload: %s", key, raw)
		}
	}
	if merged["keep"] != "yes" {
		t.Fatalf("benign credential field keep=%v, want yes", merged["keep"])
	}
}

func TestGeminiRefreshHTTPClientIsSSRFProtectedAtWiring(t *testing.T) {
	// 判别 mutation：把 wiring 的 auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	// 换成 http.DefaultClient 时，mock lookup 不会进入拨号层 SSRF guard，本测试不再拿到
	// ErrOAuthEndpointBlocked。
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", "operator-secret")
	var lookupHost string
	restoreLookup := auth.SwapOAuthIPLookupForTesting(func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookupHost = host
		return []net.IPAddr{{IP: net.ParseIP("192.168.1.1")}}, nil
	})
	t.Cleanup(restoreLookup)

	adapter, ok := credentialworker.DefaultModeAdapterRegistry().Lookup(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist)
	if !ok {
		t.Fatal("missing Gemini Code Assist refresh adapter")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	_, err := adapter.RefreshCredential(ctx, credentialworker.ModeRefreshInput{
		ProviderAccountID: 306,
		Vendor:            credentialstore.VendorGemini,
		AuthMode:          credentialstore.AuthModeCodeAssist,
		Payload: []byte(`{
			"access_token":"old",
			"refresh_token":"rt-old"
		}`),
		Now: time.Now().UTC(),
	})
	if !errors.Is(err, auth.ErrOAuthEndpointBlocked) {
		t.Fatalf("RefreshCredential err=%v, want ErrOAuthEndpointBlocked from SSRF-protected wiring", err)
	}
	if lookupHost != "oauth2.googleapis.com" {
		t.Fatalf("SSRF lookup host=%q, want oauth2.googleapis.com", lookupHost)
	}
}

func findGeminiRefreshValue(v reflect.Value) (reflect.Value, bool) {
	if !v.IsValid() {
		return reflect.Value{}, false
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		return findGeminiRefreshValue(v.Elem())
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		return findGeminiRefreshValue(v.Elem())
	}
	if v.Type() == reflect.TypeOf(adapters.GeminiRefresh{}) {
		return v, true
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	for i := 0; i < v.NumField(); i++ {
		if found, ok := findGeminiRefreshValue(v.Field(i)); ok {
			return found, true
		}
	}
	return reflect.Value{}, false
}

type geminiSSRFRoundTripFunc func(*http.Request) (*http.Response, error)

func (f geminiSSRFRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func geminiSSRFJSONResponse(status int, body map[string]any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}

func geminiSSRFForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		t.Fatalf("parse form %q: %v", string(raw), err)
	}
	return form
}
