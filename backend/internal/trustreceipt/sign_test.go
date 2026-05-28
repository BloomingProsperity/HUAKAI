package trustreceipt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestSignReceiptReturnsBase64SignatureAndFingerprint(t *testing.T) {
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	receipt := signedTestReceipt()

	sigB64, fingerprint, err := SignReceipt(signer, receipt)
	if err != nil {
		t.Fatalf("SignReceipt error: %v", err)
	}
	if sigB64 == "" {
		t.Fatal("signature is empty")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("signature len=%d want %d", len(sig), ed25519.SignatureSize)
	}
	if fingerprint != signer.Fingerprint() {
		t.Fatalf("fingerprint=%q want %q", fingerprint, signer.Fingerprint())
	}
	if len(fingerprint) != sign.PubkeyFingerprintLen {
		t.Fatalf("fingerprint len=%d want %d", len(fingerprint), sign.PubkeyFingerprintLen)
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		t.Fatalf("fingerprint must be hex: %v", err)
	}
	canonical, err := Canonical(receipt)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if !ed25519.Verify(signer.PublicKey(), canonical, sig) {
		t.Fatal("signature does not verify against canonical receipt")
	}
}

func TestSignReceiptDifferentFieldsProduceDifferentSignatures(t *testing.T) {
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	receiptA := signedTestReceipt()
	receiptB := receiptA
	receiptB.CostCents++

	sigA, _, err := SignReceipt(signer, receiptA)
	if err != nil {
		t.Fatalf("SignReceipt A error: %v", err)
	}
	sigB, _, err := SignReceipt(signer, receiptB)
	if err != nil {
		t.Fatalf("SignReceipt B error: %v", err)
	}
	if sigA == sigB {
		t.Fatalf("signatures must differ when cost_cents changes; both=%q", sigA)
	}
}

func TestSignReceiptNilSignerReturnsErr(t *testing.T) {
	sigB64, fingerprint, err := SignReceipt(nil, signedTestReceipt())
	if !errors.Is(err, ErrSignerNil) {
		t.Fatalf("error=%v want ErrSignerNil", err)
	}
	if sigB64 != "" || fingerprint != "" {
		t.Fatalf("nil signer must not return signature/fingerprint; got sig=%q fp=%q", sigB64, fingerprint)
	}
}

func signedTestReceipt() TrustReceiptV1 {
	return TrustReceiptV1{
		RequestID:       "req-sign",
		ReceiptSequence: 2,
		TenantScopeRef:  "tenant:7001",
		OccurredAt:      time.Date(2026, 5, 27, 8, 9, 10, 0, time.UTC),
		Provider:        "openai",
		RequestedModel:  "gpt-4o",
		RoutedModel:     "openai/gpt-4o",
		UpstreamModel:   "gpt-4o-2024-08-06",
		DeliveredModel:  "gpt-4o-2024-08-06",
		CostCents:       123,
		TokenCounts: TokenCounts{
			Input:  11,
			Output: 17,
			Cached: 5,
		},
		PriceSnapshot: PriceSnapshot{
			RateTableSnapshotID: 42,
			SnapshotVersion:     "rates-v1",
			CurrencyCode:        "USD",
		},
		ValidationState:           "provisional",
		RedactedMetadataAllowlist: map[string]any{},
	}
}

