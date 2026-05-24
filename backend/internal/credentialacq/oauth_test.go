package credentialacq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func hashOAuthState(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

func completeOAuthCallback(store *memorySessionStore, flowID, state, code string, exchange func(string) (acqCandidate, error)) (acqCandidate, error) {
	row, err := store.Get(flowID)
	if err != nil {
		return acqCandidate{}, err
	}
	if !row.ConsumedAt.IsZero() || row.Status == statusFinalized {
		return acqCandidate{}, errFlowReplay
	}
	if store.now().After(row.ExpiresAt) {
		_ = store.UpdateStatus(flowID, statusExpired)
		return acqCandidate{}, errFlowExpired
	}
	if !bytes.Equal(row.StateHash, hashOAuthState(state)) {
		_ = store.UpdateStatus(flowID, statusFailed)
		return acqCandidate{}, errStateMismatch
	}
	_ = store.UpdateStatus(flowID, statusCallbackReceived)
	candidate, err := exchange(code)
	if err != nil {
		_ = store.UpdateStatus(flowID, statusFailed)
		return acqCandidate{}, err
	}
	if candidate.Vendor == "" {
		candidate.Vendor = row.Vendor
	}
	if candidate.AuthMode == "" {
		candidate.AuthMode = row.AuthMode
	}
	if err := store.UpdateStatus(flowID, statusValidated); err != nil {
		return acqCandidate{}, err
	}
	return candidate, nil
}

func TestOAuthCallbackRejectsStateMismatch(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-oauth", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("expected"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := completeOAuthCallback(store, "flow-oauth", "wrong", "code", func(string) (acqCandidate, error) {
		t.Fatal("exchange must not run on state mismatch")
		return acqCandidate{}, nil
	})
	if !errors.Is(err, errStateMismatch) {
		t.Fatalf("err=%v want %v", err, errStateMismatch)
	}
	got, _ := store.Get("flow-oauth")
	if got.Status != statusFailed {
		t.Fatalf("status=%q want %q", got.Status, statusFailed)
	}
}

func TestCompleteOAuthCallbackRejectsCrossFlowStateReplay(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })

	victim, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	})
	if err != nil {
		t.Fatalf("start victim flow: %v", err)
	}
	attacker, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 202,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	})
	if err != nil {
		t.Fatalf("start attacker flow: %v", err)
	}
	if OAuthStateMatches(attacker.Session.StateHash, victim.State) {
		t.Fatal("fixture is not discriminating: replayed state unexpectedly matches attacker flow")
	}

	exchangeCalled := false
	_, session, err := CompleteOAuthCallback(context.Background(), store, attacker.Session.ID, victim.State, "attacker-code",
		func(context.Context, Session, string) (CredentialCandidate, error) {
			exchangeCalled = true
			return CredentialCandidate{Payload: []byte(`{"session_token":"should-not-run"}`)}, nil
		})
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("err=%v want %v", err, ErrStateMismatch)
	}
	if exchangeCalled {
		t.Fatal("exchange ran for replayed state")
	}
	if session.Status != StatusFailed || session.ErrorClass != "state_mismatch" {
		t.Fatalf("session status/class=%s/%s want failed/state_mismatch", session.Status, session.ErrorClass)
	}
}

func TestStartOAuthFlowPKCEVerifierEncryptedAtRest(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	result, err := StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if result.CodeVerifier == "" {
		t.Fatal("CodeVerifier is empty")
	}
	if bytes.Contains(result.Session.EncryptedPKCEVerifier, []byte(result.CodeVerifier)) {
		t.Fatalf("encrypted_pkce_verifier leaked plaintext verifier")
	}
	if strings.Contains(string(result.Session.NonceHash), result.CodeVerifier) {
		t.Fatalf("pkce metadata leaked plaintext verifier")
	}
	plain, err := store.DecryptTransientPayload(context.Background(), result.Session.EncryptedPKCEVerifier, result.Session.NonceHash, pkceAADFromSession(result.Session))
	if err != nil {
		t.Fatalf("DecryptTransientPayload: %v", err)
	}
	if string(plain) != result.CodeVerifier {
		t.Fatal("decrypted verifier did not match generated verifier")
	}
}

func TestOAuthCallbackRejectsReplayAfterConsume(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-replay", Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := completeOAuthCallback(store, "flow-replay", "ok", "code", successfulOAuthExchange); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume("flow-replay", 55); err != nil {
		t.Fatal(err)
	}
	_, err := completeOAuthCallback(store, "flow-replay", "ok", "code", successfulOAuthExchange)
	if !errors.Is(err, errFlowReplay) {
		t.Fatalf("err=%v want %v", err, errFlowReplay)
	}
}

func TestOAuthCallbackExchangeSuccessAndFailure(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-success", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	candidate, err := completeOAuthCallback(store, "flow-success", "ok", "code", successfulOAuthExchange)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeChatGPTOAuth {
		t.Fatalf("candidate target=%s/%s", candidate.Vendor, candidate.AuthMode)
	}
	got, _ := store.Get("flow-success")
	if got.Status != statusValidated {
		t.Fatalf("status=%q want %q", got.Status, statusValidated)
	}

	if err := store.Create(acqSession{
		ID: "flow-fail", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	exchangeErr := errors.New("redacted exchange failure")
	_, err = completeOAuthCallback(store, "flow-fail", "ok", "bad-code", func(string) (acqCandidate, error) {
		return acqCandidate{}, exchangeErr
	})
	if !errors.Is(err, exchangeErr) {
		t.Fatalf("err=%v want %v", err, exchangeErr)
	}
	got, _ = store.Get("flow-fail")
	if got.Status != statusFailed {
		t.Fatalf("status=%q want %q", got.Status, statusFailed)
	}
}

func TestDefaultExchangerRegistryIncludesWindsurfOAuthSessionShape(t *testing.T) {
	// Regression killed: Windsurf OAuth callback must not fall through to
	// exchanger_missing, and its fake capture payload must contain session
	// material. Mutation self-check: deleting the registry line or using an
	// access/refresh-only shape makes one side of this test fail.
	registry := DefaultExchangerRegistry()
	if _, ok := registry.Lookup("windsurf/oauth"); !ok {
		t.Fatal("windsurf/oauth exchanger missing")
	}
	session := Session{
		TenantID:          1,
		ProviderAccountID: 42,
		Vendor:            "windsurf",
		AuthMode:          "oauth",
		ActorID:           "operator-1",
	}
	candidate, err := registry.Exchange(context.Background(), session, `{"session_token":"windsurf-session-token","refresh_token":"windsurf-refresh-token"}`)
	if err != nil {
		t.Fatalf("Exchange windsurf/oauth: %v", err)
	}
	if candidate.Vendor != "windsurf" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 42 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	if !strings.Contains(string(candidate.Payload), "windsurf-session-token") {
		t.Fatalf("candidate payload=%s, want session token material", string(candidate.Payload))
	}

	_, err = registry.Exchange(context.Background(), session, `{"access_token":"access-only","refresh_token":"refresh-only"}`)
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("access/refresh-only Windsurf payload err=%v, want ErrInvalidTokenShape", err)
	}
}

func successfulOAuthExchange(code string) (acqCandidate, error) {
	if code == "" {
		return acqCandidate{}, errors.New("missing code")
	}
	return acqCandidate{
		Payload: []byte(`{"session_token":"session-value","refresh_token":"refresh-value"}`),
		RedactedContext: map[string]any{
			"account_email_hash": "sha256:example",
		},
	}, nil
}
