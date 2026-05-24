package anthropicoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

func TestStartFlowBuildsAnthropicAuthorizeURL(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	store := testStore(t, now)
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/admin/oauth/anthropic/callback")}

	start, err := exchanger.StartOAuthFlow(context.Background(), store, credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, credentialacq.OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if start.Session.Vendor != credentialstore.VendorAnthropic || start.Session.AuthMode != credentialstore.AuthModeClaudeAIOAuth {
		t.Fatalf("session target=%s/%s", start.Session.Vendor, start.Session.AuthMode)
	}
	if start.CodeVerifier == "" || start.CodeChallenge == "" {
		t.Fatalf("empty PKCE verifier/challenge")
	}
	if strings.Contains(start.AuthorizeURL, start.CodeVerifier) {
		t.Fatalf("authorize URL leaked verifier: %s", start.AuthorizeURL)
	}
	for _, want := range []string{
		"https://claude.ai/oauth/authorize?",
		"client_id=" + AnthropicPublicCLIClientID,
		"code_challenge_method=S256",
		"response_type=code",
		"state=",
	} {
		if !strings.Contains(start.AuthorizeURL, want) {
			t.Fatalf("authorize URL %q missing %q", start.AuthorizeURL, want)
		}
	}
}

func TestCallbackRejectsCrossFlowStateReplay(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 5, 0, 0, time.UTC)
	store := testStore(t, now)
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/callback")}
	registry := credentialacq.NewExchangerRegistry()
	if err := RegisterInto(registry, exchanger); err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	victim := startAnthropicFlow(t, store, exchanger, 201)
	attacker := startAnthropicFlow(t, store, exchanger, 202)
	if credentialacq.OAuthStateMatches(attacker.Session.StateHash, victim.State) {
		t.Fatal("fixture is not discriminating: victim state matches attacker flow")
	}

	_, session, err := credentialacq.CompleteOAuthCallbackWithRegistry(context.Background(), store, attacker.Session.ID, victim.State, "code", registry)
	if !errors.Is(err, credentialacq.ErrStateMismatch) {
		t.Fatalf("err=%v want %v", err, credentialacq.ErrStateMismatch)
	}
	if session.Status != credentialacq.StatusFailed || session.ErrorClass != "state_mismatch" {
		t.Fatalf("session status/class=%s/%s want failed/state_mismatch", session.Status, session.ErrorClass)
	}
}

func TestCallbackUsesStoredPKCEVerifierForExchange(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 10, 0, 0, time.UTC)
	store := testStore(t, now)
	start := credentialacq.OAuthStartResult{}
	var sawVerifier atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got["code_verifier"] != start.CodeVerifier {
			return jsonHTTPResponse(t, http.StatusBadRequest, map[string]any{"error": "bad_verifier"}), nil
		}
		sawVerifier.Store(true)
		return jsonHTTPResponse(t, http.StatusOK, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"id_token":      "id-1",
			"expires_in":    3600,
			"account":       map[string]any{"email_address": "owner@example.test"},
		}), nil
	})}
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/callback"), HTTPClient: client, Now: func() time.Time { return now }}
	registry := credentialacq.NewExchangerRegistry()
	if err := RegisterInto(registry, exchanger); err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	start = startAnthropicFlow(t, store, exchanger, 301)

	candidate, session, err := credentialacq.CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if !sawVerifier.Load() {
		t.Fatal("token endpoint did not see the stored PKCE verifier")
	}
	if session.Status != credentialacq.StatusValidated {
		t.Fatalf("status=%s want validated", session.Status)
	}
	var payload Token
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("decode candidate payload: %v", err)
	}
	if payload.AccessToken != "access-1" || payload.RefreshToken != "refresh-1" || payload.IDToken != "id-1" {
		t.Fatalf("payload tokens not preserved: %+v", payload)
	}
	if payload.Email != "owner@example.test" || payload.AuthMode != credentialstore.AuthModeClaudeAIOAuth {
		t.Fatalf("payload metadata email/auth_mode=%q/%q", payload.Email, payload.AuthMode)
	}
}

func TestDefaultTokenExchangeClientUsesAnthropicMimicryTransport(t *testing.T) {
	client := (Exchanger{}).httpClient()
	if client == nil || client.Transport == nil {
		t.Fatal("default token exchange client must install a mimicry transport")
	}
	if client == http.DefaultClient || client.Transport == http.DefaultTransport {
		t.Fatal("default token exchange client must not silently use stdlib http")
	}
	if got := fmt.Sprintf("%T", client.Transport); !strings.Contains(got, "mimicry.roundTripper") {
		t.Fatalf("default token exchange transport = %s, want mimicry uTLS roundTripper", got)
	}
}

func TestTokenExchangeMissingMimicryProfileWarnsBeforeAuditOnlyFallback(t *testing.T) {
	var logs bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(oldDefault)

	exchanger := Exchanger{MimicryRegistry: mimicry.NewTemplateRegistry()}
	client := exchanger.httpClient()
	if client == nil {
		t.Fatal("audit-only fallback must still return a client")
	}
	if client == http.DefaultClient {
		t.Fatal("fallback must be explicit, not a silent return of http.DefaultClient")
	}
	got := logs.String()
	for _, want := range []string{
		"anthropicoauth mimicry transport unavailable",
		"reason_class=mimicry_transport_unavailable",
		mimicry.SidecarProfileAnthropicCLIMimicryV1,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log %q missing %q", got, want)
		}
	}
}

func TestCallbackFailureRecordsExchangeAuditState(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 15, 0, 0, time.UTC)
	store := testStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(t, http.StatusBadGateway, map[string]any{"error": "upstream_down"}), nil
	})}
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/callback"), HTTPClient: client, Now: func() time.Time { return now }}
	registry := credentialacq.NewExchangerRegistry()
	if err := RegisterInto(registry, exchanger); err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	start := startAnthropicFlow(t, store, exchanger, 401)

	_, session, err := credentialacq.CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "bad-code", registry)
	if err == nil {
		t.Fatal("expected exchange failure")
	}
	if session.Status != credentialacq.StatusFailed || session.ErrorClass != "exchange_failed" {
		t.Fatalf("session status/class=%s/%s want failed/exchange_failed", session.Status, session.ErrorClass)
	}
	if strings.Contains(session.ErrorMessageRedacted, "bad-code") {
		t.Fatalf("error message leaked auth code: %q", session.ErrorMessageRedacted)
	}
}

func TestExchangeRejectsCrossVendorTokenShape(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 20, 0, 0, time.UTC)
	store := testStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(t, http.StatusOK, map[string]any{
			"session_token": "chatgpt-session-only",
			"expires_in":    3600,
		}), nil
	})}
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/callback"), HTTPClient: client, Now: func() time.Time { return now }}
	registry := credentialacq.NewExchangerRegistry()
	if err := RegisterInto(registry, exchanger); err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	start := startAnthropicFlow(t, store, exchanger, 501)

	_, _, err := credentialacq.CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "code", registry)
	if !errors.Is(err, credentialacq.ErrInvalidTokenShape) {
		t.Fatalf("err=%v want %v", err, credentialacq.ErrInvalidTokenShape)
	}
}

func testStore(t *testing.T, now time.Time) *credentialacq.PostgresSessionStore {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return credentialacq.NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
}

func startAnthropicFlow(t *testing.T, store *credentialacq.PostgresSessionStore, exchanger Exchanger, accountID int64) credentialacq.OAuthStartResult {
	t.Helper()
	start, err := exchanger.StartOAuthFlow(context.Background(), store, credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: accountID,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, credentialacq.OAuthClientConfig{})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	return start
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(t *testing.T, status int, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}
