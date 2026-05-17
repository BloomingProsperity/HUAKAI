package auditledger

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func testPrivateKey(seedByte byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seedByte}, ed25519.SeedSize))
}

func TestLocalEd25519SignerSignVerifyAndTamper(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalEd25519Signer(testPrivateKey(1), nil)
	if err != nil {
		t.Fatalf("NewLocalEd25519Signer: %v", err)
	}
	payload := []byte("canonical-entry-hash")
	sig, fp, err := signer.Sign(ctx, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(fp) != 16 {
		t.Fatalf("fingerprint length: got %d want 16", len(fp))
	}
	ok, err := signer.Verify(ctx, payload, sig, fp)
	if err != nil || !ok {
		t.Fatalf("Verify pass: ok=%v err=%v", ok, err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0xff
	ok, err = signer.Verify(ctx, tampered, sig, fp)
	if err != nil {
		t.Fatalf("Verify tamper returned error: %v", err)
	}
	if ok {
		t.Fatal("tampered payload verified")
	}
}

func TestLocalEd25519SignerEnvOverridesKeyProvider(t *testing.T) {
	ctx := context.Background()
	envPriv := testPrivateKey(2)
	t.Setenv(TrustLedgerPrivateKeyEnv, base64.StdEncoding.EncodeToString(envPriv))
	provider, err := credentialstore.NewStaticKeyProvider("provider-seed", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	signer, err := NewLocalEd25519SignerFromEnv(ctx, provider)
	if err != nil {
		t.Fatalf("NewLocalEd25519SignerFromEnv: %v", err)
	}
	if got, want := signer.Fingerprint(), PubkeyFingerprint(envPriv.Public().(ed25519.PublicKey)); got != want {
		t.Fatalf("env key did not win: got %s want %s", got, want)
	}
}

func TestLocalEd25519SignerKeyProviderSeed(t *testing.T) {
	ctx := context.Background()
	provider, err := credentialstore.NewStaticKeyProvider("provider-seed", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	signer, err := NewLocalEd25519SignerFromEnv(ctx, provider)
	if err != nil {
		t.Fatalf("NewLocalEd25519SignerFromEnv: %v", err)
	}
	wantPriv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))
	if got, want := signer.Fingerprint(), PubkeyFingerprint(wantPriv.Public().(ed25519.PublicKey)); got != want {
		t.Fatalf("provider seed fingerprint: got %s want %s", got, want)
	}
}

func TestLocalEd25519SignerPubkeyRotationVerify(t *testing.T) {
	ctx := context.Background()
	oldPriv := testPrivateKey(4)
	newPriv := testPrivateKey(5)
	oldPub := oldPriv.Public().(ed25519.PublicKey)
	oldSigner, err := NewLocalEd25519Signer(oldPriv, nil)
	if err != nil {
		t.Fatalf("old signer: %v", err)
	}
	payload := []byte("canonical-entry-hash")
	oldSig, oldFP, err := oldSigner.Sign(ctx, payload)
	if err != nil {
		t.Fatalf("old sign: %v", err)
	}
	envDoc, err := json.Marshal(map[string]any{
		"rotated": []map[string]string{{
			"fingerprint":   oldFP,
			"pubkey_base64": base64.StdEncoding.EncodeToString(oldPub),
			"status":        "rotated",
			"valid_until":   "2026-06-16T00:00:00Z",
		}},
	})
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	t.Setenv(TrustLedgerPubkeysEnv, string(envDoc))
	records, err := LoadPublicKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPublicKeysFromEnv: %v", err)
	}
	newSigner, err := NewLocalEd25519Signer(newPriv, records)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	ok, err := newSigner.Verify(ctx, payload, oldSig, oldFP)
	if err != nil || !ok {
		t.Fatalf("rotated old pubkey verify: ok=%v err=%v", ok, err)
	}
	_, newFP, err := newSigner.Sign(ctx, payload)
	if err != nil {
		t.Fatalf("new sign: %v", err)
	}
	if newFP == oldFP {
		t.Fatal("new entries must use active new pubkey fingerprint")
	}
}
