package sign

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestGenerateKey_RoundTrip(t *testing.T) {
	s, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if s == nil {
		t.Fatal("nil signer")
	}
	msg := []byte("hello huakai trust chain")
	sig := s.Sign(msg)
	if len(sig) != ed25519.SignatureSize {
		t.Errorf("signature size: got %d want %d", len(sig), ed25519.SignatureSize)
	}
	if err := Verify(s.PublicKey(), msg, sig); err != nil {
		t.Errorf("verify happy path failed: %v", err)
	}
}

func TestVerify_TamperedMessageFails(t *testing.T) {
	s, _ := GenerateKey()
	msg := []byte("original")
	sig := s.Sign(msg)
	tampered := []byte("tampered")
	if err := Verify(s.PublicKey(), tampered, sig); err == nil {
		t.Error("tampered message must fail verify")
	}
}

func TestVerify_WrongPubkeyFails(t *testing.T) {
	s1, _ := GenerateKey()
	s2, _ := GenerateKey()
	msg := []byte("x")
	sig := s1.Sign(msg)
	if err := Verify(s2.PublicKey(), msg, sig); err == nil {
		t.Error("wrong pubkey must fail verify")
	}
}

func TestVerify_TamperedSignatureFails(t *testing.T) {
	s, _ := GenerateKey()
	msg := []byte("x")
	sig := s.Sign(msg)
	sig[0] ^= 0xff
	if err := Verify(s.PublicKey(), msg, sig); err == nil {
		t.Error("tampered signature must fail verify")
	}
}

func TestVerify_LengthGuards(t *testing.T) {
	s, _ := GenerateKey()
	msg := []byte("x")
	sig := s.Sign(msg)

	if err := Verify(make(ed25519.PublicKey, 31), msg, sig); err != ErrInvalidPublicKey {
		t.Errorf("short pubkey: got %v want ErrInvalidPublicKey", err)
	}
	if err := Verify(s.PublicKey(), msg, []byte{0x01, 0x02}); err != ErrInvalidSignature {
		t.Errorf("short sig: got %v want ErrInvalidSignature", err)
	}
}

func TestFingerprint_StableAndLen(t *testing.T) {
	s, _ := GenerateKey()
	fp1 := s.Fingerprint()
	fp2 := Fingerprint(s.PublicKey())
	if fp1 != fp2 {
		t.Errorf("fingerprint mismatch: signer=%q top-level=%q", fp1, fp2)
	}
	if len(fp1) != PubkeyFingerprintLen {
		t.Errorf("fingerprint len: got %d want %d", len(fp1), PubkeyFingerprintLen)
	}
	// 只能是 hex 字符
	for _, c := range fp1 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("fingerprint contains non-hex char: %q", fp1)
		}
	}
}

func TestFingerprint_InvalidPubkeyEmpty(t *testing.T) {
	if Fingerprint(make(ed25519.PublicKey, 31)) != "" {
		t.Error("Fingerprint of invalid-length pubkey must return empty")
	}
}

func TestNewSignerFromKey_RejectShort(t *testing.T) {
	if _, err := NewSignerFromKey(make(ed25519.PrivateKey, 10)); err != ErrInvalidPrivateKey {
		t.Errorf("got %v want ErrInvalidPrivateKey", err)
	}
}

func TestSign_TwoSignersDifferentKeys(t *testing.T) {
	s1, _ := GenerateKey()
	s2, _ := GenerateKey()
	msg := []byte("same message")
	sig1 := s1.Sign(msg)
	sig2 := s2.Sign(msg)
	if bytes.Equal(sig1, sig2) {
		t.Error("different keys must produce different signatures for same msg (probabilistic certainty)")
	}
}

func TestPublicKey_IsCopy(t *testing.T) {
	s, _ := GenerateKey()
	pub := s.PublicKey()
	pub[0] ^= 0xff
	// 篡改外部副本不应影响内部签名行为：用 s.PublicKey() 重新拿，应该能 verify。
	msg := []byte("x")
	sig := s.Sign(msg)
	if err := Verify(s.PublicKey(), msg, sig); err != nil {
		t.Errorf("internal pub should be intact, got %v", err)
	}
}
