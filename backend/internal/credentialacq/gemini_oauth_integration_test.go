package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// 缺陷：admin callback 入口若仍走 fake JSON 占位，会绕过 stored PKCE verifier 并保存攻击者提交的 token。
// 判别 mutation：把 GEM-1 registry/exchange 改回 fake 或破坏 AAD 解密时，本测试必须变红。
func TestGeminiCodeAssistAdminCallbackEndToEnd(t *testing.T) {
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	store, _ := newGeminiOAuthTestStore(t, now)
	tokenCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("code") != "mock-code" || r.PostForm.Get("code_verifier") == "" {
			t.Fatalf("token form=%v want callback code and stored PKCE verifier", r.PostForm)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-admin-gemini","refresh_token":"RT-admin-gemini","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	adminCallbackAllowlist := []string{"https://huakai.example/admin/v1/credentials/oauth-callback"}
	exchanger := NewGeminiPublicCLIOAuthExchangerWithClientAndAdminCallbackAllowlist(credentialstore.AuthModeCodeAssist, client, adminCallbackAllowlist).(geminiPublicCLIOAuthExchanger)
	exchanger.now = func() time.Time { return now }
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	start, err := exchanger.StartOAuthFlow(context.Background(), store, geminiStartInput(credentialstore.AuthModeCodeAssist, 801), OAuthClientConfig{
		ClientSecret: "operator-secret",
		RedirectURI:  "https://huakai.example/admin/v1/credentials/oauth-callback",
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "mock-code", registry)
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
		"access_token":           "AT-admin-gemini",
		"refresh_token":          "RT-admin-gemini",
		"client_identity_source": geminiApprovedProfileSource,
	} {
		if got := stringFieldFromAny(payload[key]); got != want {
			t.Fatalf("payload[%s]=%q want %q; payload=%v", key, got, want, payload)
		}
	}
	if strings.Contains(string(candidate.Payload), "FAKE") {
		t.Fatalf("payload contains fake callback material: %s", candidate.Payload)
	}
	if got := stringFieldFromAny(candidate.RedactedContext["client_identity_source"]); got != geminiApprovedProfileSource {
		t.Fatalf("redacted context source=%q want %q", got, geminiApprovedProfileSource)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}

	tampered := start.Session
	tampered.AuthMode = credentialstore.AuthModeGoogleOne
	_, err = exchanger.ExchangeOAuthCodeWithStore(context.Background(), store, tampered, start.State, "mock-code")
	if err == nil {
		t.Fatal("tampered AAD decrypted successfully; expected failure")
	}
	if errors.Is(err, ErrOAuthExchangerMissing) {
		t.Fatalf("err=%v should be decrypt/profile failure, not fake exchanger missing", err)
	}
}
