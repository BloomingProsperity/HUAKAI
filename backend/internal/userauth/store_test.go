package userauth

import (
	"bytes"
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestAuthOAuthFlowEncryptsPKCEVerifierAtRest(t *testing.T) {
	keys, err := credentialstore.NewStaticKeyProvider("pkce-test-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	store := NewPostgresStore(nil).WithKeyProvider(keys)
	challenge := OAuthFlowChallenge{
		ID: "00000000-0000-0000-0000-000000000123", TenantID: 1,
		Provider: SocialProviderGoogle, PKCEVerifier: "raw-pkce-verifier",
		StateHash: []byte("state-hash"),
	}
	ciphertext, err := store.encryptPKCEVerifier(context.Background(), challenge)
	if err != nil {
		t.Fatalf("encryptPKCEVerifier: %v", err)
	}
	if len(ciphertext) == 0 {
		t.Fatal("encrypted PKCE payload missing")
	}
	if bytes.Contains(ciphertext, []byte(challenge.PKCEVerifier)) {
		t.Fatal("encrypted PKCE storage leaked raw verifier bytes")
	}
	flow := OAuthFlowSession{
		ID: challenge.ID, TenantID: challenge.TenantID, Provider: challenge.Provider,
		StateHash: challenge.StateHash, PKCEVerifierCiphertext: ciphertext,
	}
	verifier, err := store.decryptPKCEVerifier(context.Background(), flow)
	if err != nil {
		t.Fatalf("decryptPKCEVerifier: %v", err)
	}
	if verifier != challenge.PKCEVerifier {
		t.Fatalf("PKCE verifier=%q want %q", verifier, challenge.PKCEVerifier)
	}
	legacy := OAuthFlowSession{PKCEVerifier: "legacy-plain"}
	verifier, err = store.decryptPKCEVerifier(context.Background(), legacy)
	if err != nil {
		t.Fatalf("legacy plaintext compatibility: %v", err)
	}
	if verifier != "legacy-plain" {
		t.Fatalf("legacy verifier=%q want legacy-plain", verifier)
	}
}
