package credentialstore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCipherRoundTripAndAADMismatch(t *testing.T) {
	key := strings.Repeat("k", 32)
	provider, err := NewStaticKeyProvider("local", []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCipher(provider)
	aad := AAD{TenantID: 7, ProviderAccountID: 9, Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey, Version: 1}
	env, err := c.Encrypt(context.Background(), []byte(`{"api_key":"sk-secret"}`), aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(string(env.Ciphertext), "sk-secret") {
		t.Fatalf("ciphertext leaked plaintext: %q", string(env.Ciphertext))
	}
	got, err := c.Decrypt(context.Background(), env, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != `{"api_key":"sk-secret"}` {
		t.Fatalf("plaintext=%s", string(got))
	}
	aad.ProviderAccountID = 10
	if _, err := c.Decrypt(context.Background(), env, aad); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("AAD mismatch err=%v want ErrDecryptFailed", err)
	}
}

func TestDecodeKeyMaterialAcceptsBase64AndHex(t *testing.T) {
	base64Key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	if key, err := DecodeKeyMaterial(base64Key); err != nil || len(key) != 32 {
		t.Fatalf("base64 key len=%d err=%v", len(key), err)
	}
	hexKey := "3031323334353637383961626364656630313233343536373839616263646566"
	if key, err := DecodeKeyMaterial(hexKey); err != nil || len(key) != 32 {
		t.Fatalf("hex key len=%d err=%v", len(key), err)
	}
}
