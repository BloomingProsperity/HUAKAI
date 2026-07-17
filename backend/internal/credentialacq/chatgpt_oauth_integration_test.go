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

// 缺陷：admin callback 入口若仍走 fake JSON 占位，会绕过 stored PKCE verifier 并保存攻击者提交的 token。
// 判别 mutation：把 ChatGPT registry/exchange 改回 fake 或破坏 AAD 解密时，本测试必须变红。
func TestChatGPTOAuthAdminCallbackEndToEnd(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 20, 0, 0, time.UTC)
	store, _ := newChatGPTOAuthTestStore(t, now)
	tokenCalls := 0
	var wantTokenRedirectURI string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		if r.URL.String() != chatgptOAuthTokenURL {
			t.Fatalf("token URL=%s want %s", r.URL.String(), chatgptOAuthTokenURL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("code") != "mock-code" || r.PostForm.Get("code_verifier") == "" {
			t.Fatalf("token form=%v want callback code and stored PKCE verifier", r.PostForm)
		}
		if got := r.PostForm.Get("redirect_uri"); got != wantTokenRedirectURI {
			t.Fatalf("token redirect_uri=%q want authorize redirect_uri %q", got, wantTokenRedirectURI)
		}
		if r.PostForm.Get("client_secret") != "" {
			t.Fatalf("ChatGPT PKCE-only callback sent client_secret: %v", r.PostForm)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"AT-admin-chatgpt",
				"refresh_token":"RT-admin-chatgpt",
				"expires_in":3600,
				"token_type":"Bearer",
				"chatgpt_user_id":"user-admin",
				"chatgpt_plan_type":"Team",
				"chatgpt_account_id":"acct-admin"
			}`)),
		}, nil
	})}
	adminCallbackAllowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	exchanger := NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(client, adminCallbackAllowlist).(chatgptOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	start, err := exchanger.StartOAuthFlow(context.Background(), store, chatgptStartInput(905), OAuthClientConfig{
		RedirectURI: "https://huakai.example/admin/v1/credentials/oauth-callback",
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	wantTokenRedirectURI = chatGPTAuthorizeRedirectURI(t, start.AuthorizeURL)
	callbackURL := chatGPTProviderCallbackURLFromAuthorize(t, start.AuthorizeURL, start.State, "mock-code")
	callbackQuery := callbackURL.Query()
	if got := callbackQuery.Get("flow_id"); got != start.Session.ID {
		t.Fatalf("provider callback flow_id=%q want preserved session id %q; callback=%s", got, start.Session.ID, callbackURL.String())
	}
	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, callbackQuery.Get("flow_id"), callbackQuery.Get("state"), callbackQuery.Get("code"), registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if session.Status != StatusValidated {
		t.Fatalf("status=%s want %s", session.Status, StatusValidated)
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls=%d want 1", tokenCalls)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	for key, want := range map[string]string{
		"access_token":           "AT-admin-chatgpt",
		"refresh_token":          "RT-admin-chatgpt",
		"chatgpt_user_id":        "user-admin",
		"chatgpt_plan_type":      "Team",
		"chatgpt_account_id":     "acct-admin",
		"client_identity_source": chatgptApprovedProfileSource,
	} {
		if got := stringFieldFromAny(payload[key]); got != want {
			t.Fatalf("payload[%s]=%q want %q; payload=%v", key, got, want, payload)
		}
	}
	if strings.Contains(string(candidate.Payload), "FAKE") {
		t.Fatalf("payload contains fake callback material: %s", candidate.Payload)
	}
	if got := stringFieldFromAny(candidate.RedactedContext["client_identity_source"]); got != chatgptApprovedProfileSource {
		t.Fatalf("redacted context source=%q want %q", got, chatgptApprovedProfileSource)
	}
	if got := stringFieldFromAny(candidate.RedactedContext["chatgpt_plan_type_class"]); got != "Team" {
		t.Fatalf("redacted plan class=%q want Team", got)
	}
	if candidate.ExternalAccountID != "acct-admin" || candidate.ExternalSubjectID != "user-admin" {
		t.Fatalf("candidate identity=%+v，期望保留账号作用域和个人主体", candidate)
	}
	if _, ok := candidate.RedactedContext["chatgpt_user_id"]; ok {
		t.Fatalf("redacted context leaked chatgpt_user_id: %v", candidate.RedactedContext)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}

	tampered := start.Session
	tampered.AuthMode = credentialstore.AuthModeCodexCLIOAuth
	_, err = exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, tampered, start.State, "mock-code")
	if err == nil {
		t.Fatal("tampered AAD decrypted successfully; expected failure")
	}
	if errors.Is(err, ErrOAuthExchangerMissing) {
		t.Fatalf("err=%v should be decrypt/profile failure, not fake exchanger missing", err)
	}
}

func chatGPTAuthorizeRedirectURI(t *testing.T, authorizeURL string) string {
	t.Helper()
	authorize, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorize URL parse: %v url=%q", err, authorizeURL)
	}
	callbackRaw := authorize.Query().Get("redirect_uri")
	if callbackRaw == "" {
		t.Fatalf("authorize URL missing redirect_uri: %s", authorizeURL)
	}
	return callbackRaw
}

func chatGPTProviderCallbackURLFromAuthorize(t *testing.T, authorizeURL, state, code string) *url.URL {
	t.Helper()
	callbackRaw := chatGPTAuthorizeRedirectURI(t, authorizeURL)
	callback, err := url.Parse(callbackRaw)
	if err != nil {
		t.Fatalf("callback redirect_uri parse: %v raw=%q", err, callbackRaw)
	}
	// 模拟真实 provider：只在已有 redirect_uri query 上追加 state/code，
	// 不额外替 HUAKAI 拼 flow_id。
	q := callback.Query()
	q.Set("state", state)
	q.Set("code", code)
	callback.RawQuery = q.Encode()
	return callback
}
