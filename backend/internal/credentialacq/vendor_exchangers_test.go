package credentialacq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestDefaultExchangerRegistryIncludesAntigravityOAuthAlias(t *testing.T) {
	// Regression killed: Antigravity acquisition must be reachable through the
	// vendor-native antigravity/oauth key, not only the legacy
	// gemini/antigravity credentialstore mode.
	registry := DefaultExchangerRegistry()
	candidate, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 42, Vendor: "antigravity", AuthMode: "oauth", ActorID: "owner",
	}, `{"session_token":"ag-session"}`)
	if err != nil {
		t.Fatalf("Exchange antigravity/oauth: %v", err)
	}
	if candidate.Vendor != "antigravity" || candidate.AuthMode != "oauth" {
		t.Fatalf("candidate mode=%s/%s, want antigravity/oauth", candidate.Vendor, candidate.AuthMode)
	}
	var payload map[string]string
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["session_token"] != "ag-session" {
		t.Fatalf("session_token=%q, want ag-session", payload["session_token"])
	}
}

func TestAntigravityOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q, want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"ag-access","refresh_token":"ag-refresh","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := authorizationCodeOAuthExchanger{
		vendor: credentialstore.VendorAntigravity, authMode: credentialstore.AuthModeOAuth,
		shape: TokenShapeAnySessionOrAccess, client: client, now: func() time.Time { return now },
	}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger("antigravity/oauth", exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 501,
		Vendor: "antigravity", AuthMode: "oauth",
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://antigravity.example.test/authorize", TokenURL: "https://antigravity.example.test/token",
		ClientID: "ag-client", RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes: []string{"scope-a", "scope-b"}, Source: ClientSourceOperatorConfig,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "ag-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if session.Status != StatusValidated {
		t.Fatalf("status=%s want %s", session.Status, StatusValidated)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "ag-auth-code",
		"client_id":     "ag-client",
		"redirect_uri":  "http://127.0.0.1:1455/auth/callback",
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
	}
	if candidate.Vendor != "antigravity" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 501 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["session_token"] != "ag-access" || payload["refresh_token"] != "ag-refresh" {
		t.Fatalf("payload=%v, want access copied to session_token and refresh preserved", payload)
	}
}

func TestGeminiOperatorOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 25, 8, 10, 0, 0, time.UTC)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"gem-access","refresh_token":"gem-refresh","scope":"profile email","expires_in":1800}`)),
		}, nil
	})}
	exchanger := authorizationCodeOAuthExchanger{
		vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeOAuth,
		shape: TokenShapeAnySessionOrAccess, client: client, now: func() time.Time { return now },
	}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger("gemini/oauth", exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("b", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 502,
		Vendor: "gemini", AuthMode: "oauth",
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://google.example.test/oauth2/auth", TokenURL: "https://google.example.test/oauth2/token",
		ClientID: "gem-client", RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes: []string{"profile", "email"}, Source: ClientSourceOperatorConfig,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, _, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "gem-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if gotForm.Get("code") != "gem-auth-code" || gotForm.Get("client_id") != "gem-client" || gotForm.Get("code_verifier") != start.CodeVerifier {
		t.Fatalf("form=%v, want code/client_id/code_verifier from flow", gotForm)
	}
	if candidate.Vendor != "gemini" || candidate.AuthMode != "oauth" {
		t.Fatalf("candidate mode=%s/%s, want gemini/oauth", candidate.Vendor, candidate.AuthMode)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	payload := string(candidate.Payload)
	if !strings.Contains(payload, "gem-access") || !strings.Contains(payload, "gem-refresh") {
		t.Fatalf("payload=%s, want exchanged Google token material", payload)
	}
}
