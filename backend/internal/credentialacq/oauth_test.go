package credentialacq

import (
	"bytes"
	"crypto/sha256"
	"errors"
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
