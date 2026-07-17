package credentialstore

import (
	"context"
	"encoding/hex"
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

func TestCipherOptionalContextBindsShortLivedEnvelopeWithoutChangingLegacyAAD(t *testing.T) {
	provider, err := NewStaticKeyProvider("local", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	cipher := NewCipher(provider)
	legacy := AAD{TenantID: 7, ProviderAccountID: 9, Vendor: VendorAnthropic, AuthMode: AuthModeClaudeAIOAuth, Version: 1}
	legacyEnvelope, err := cipher.Encrypt(t.Context(), []byte("legacy"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := cipher.Decrypt(t.Context(), legacyEnvelope, legacy); err != nil || string(plaintext) != "legacy" {
		t.Fatalf("旧 AAD round-trip plaintext=%q err=%v", plaintext, err)
	}
	bound := legacy
	bound.Context = "account-credential-intake:first"
	boundEnvelope, err := cipher.Encrypt(t.Context(), []byte("short-lived"), bound)
	if err != nil {
		t.Fatal(err)
	}
	other := bound
	other.Context = "account-credential-intake:second"
	if _, err := cipher.Decrypt(t.Context(), boundEnvelope, other); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("更换会话上下文后 err=%v，期望 ErrDecryptFailed", err)
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

func TestCredentialMaterialFingerprintIsStableScopedAndPlaintextFree(t *testing.T) {
	token := "refresh-token-with-high-entropy-material"
	firstPayload := []byte(`{"access_token":"access-a","refresh_token":"` + token + `","label":"one"}`)
	secondPayload := []byte(`{"session_token":"session-b","refresh_token":"` + token + `","label":"two"}`)

	got := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, firstPayload)
	if got == "" || CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, secondPayload) != got {
		t.Fatalf("同一长期刷新材料的指纹不稳定：%q", got)
	}
	decoded, err := hex.DecodeString(got)
	if err != nil || len(decoded) != 32 || strings.Contains(got, token) {
		t.Fatalf("fingerprint=%q len=%d err=%v", got, len(decoded), err)
	}
	for name, candidate := range map[string]string{
		"tenant": CredentialMaterialFingerprint(8, VendorOpenAI, AuthModeCodexCLIOAuth, firstPayload),
		"vendor": CredentialMaterialFingerprint(7, VendorGemini, AuthModeCodexCLIOAuth, firstPayload),
		"mode":   CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeChatGPTOAuth, firstPayload),
		"token":  CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, []byte(`{"refresh_token":"another-refresh"}`)),
	} {
		if candidate == got {
			t.Fatalf("%s 边界变化后指纹未隔离", name)
		}
	}
}

func TestCredentialMaterialFingerprintUsesOnlyRuntimeSecretMaterial(t *testing.T) {
	apiKey := "api-key-with-high-entropy-material"
	first := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeAPIKey, []byte(`{"api_key":"`+apiKey+`","label":"one"}`))
	second := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeAPIKey, []byte(`{"label":"two","api_key":"`+apiKey+`"}`))
	if first == "" || first != second {
		t.Fatalf("同一 API key 因无关元数据变化而漂移：first=%q second=%q", first, second)
	}
	if got := CredentialMaterialFingerprint(7, VendorGemini, AuthModeVertexSA,
		[]byte(`{"client_email":"runtime@example.test","metadata_token_endpoint":"http://metadata/token","project_id":"one"}`)); got != "" {
		t.Fatalf("低熵 metadata-only 身份不得生成凭据材料指纹：%q", got)
	}
	for name, payload := range map[string][]byte{
		"invalid_json": []byte(`{"access_token":`),
		"empty_object": []byte(`{}`),
	} {
		if got := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, payload); got != "" {
			t.Fatalf("%s fingerprint=%q，期望为空", name, got)
		}
	}
	if got := CredentialMaterialFingerprint(0, VendorOpenAI, AuthModeCodexCLIOAuth, []byte(`{"access_token":"token"}`)); got != "" {
		t.Fatalf("缺少 tenant 时 fingerprint=%q，期望为空", got)
	}
}

func TestClaudeSetupTokenFingerprintIsTenantScoped(t *testing.T) {
	payload := []byte(`{"setup_token":"long-lived-setup-material"}`)
	got := CredentialMaterialFingerprint(7, VendorAnthropic, AuthModeClaudeSetupToken, payload)
	if got == "" {
		t.Fatal("setup token 必须生成去重指纹")
	}
	if other := CredentialMaterialFingerprint(8, VendorAnthropic, AuthModeClaudeSetupToken, payload); other == got {
		t.Fatal("setup token 指纹未按 tenant 隔离")
	}
	if strings.Contains(got, "long-lived-setup-material") {
		t.Fatal("setup token 指纹泄漏明文")
	}
}
