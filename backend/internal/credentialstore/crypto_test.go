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
	accessPayload := []byte(`{"access_token":"access-a","refresh_token":"` + token + `","label":"one"}`)
	sessionPayload := []byte(`{"session_token":"session-b","refresh_token":"` + token + `","label":"two"}`)

	got := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, accessPayload)
	if got == "" {
		t.Fatal("refresh token 指纹为空")
	}
	if sameToken := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, sessionPayload); sameToken != got {
		t.Fatalf("同一长期刷新材料因短期 token 或无关元数据变化而漂移：access=%s session=%s", got, sameToken)
	}
	decoded, err := hex.DecodeString(got)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("fingerprint=%q 不是 32 字节 SHA-256：len=%d err=%v", got, len(decoded), err)
	}
	if strings.Contains(got, token) {
		t.Fatalf("fingerprint 泄漏 token：%s", got)
	}

	for name, candidate := range map[string]string{
		"tenant":    CredentialMaterialFingerprint(8, VendorOpenAI, AuthModeCodexCLIOAuth, accessPayload),
		"vendor":    CredentialMaterialFingerprint(7, VendorGemini, AuthModeCodexCLIOAuth, accessPayload),
		"auth_mode": CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeChatGPTOAuth, accessPayload),
		"token": CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth,
			[]byte(`{"access_token":"another-access","refresh_token":"another-refresh"}`)),
	} {
		if candidate == got {
			t.Fatalf("%s 边界变化后指纹未隔离：%s", name, candidate)
		}
	}
}

func TestCredentialMaterialFingerprintCoversAPIKeyAndRefreshOnly(t *testing.T) {
	apiKey := "api-key-with-high-entropy-material"
	first := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeAPIKey,
		[]byte(`{"api_key":"`+apiKey+`","label":"one"}`))
	second := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeAPIKey,
		[]byte(`{"label":"two","api_key":"`+apiKey+`"}`))
	if first == "" || first != second {
		t.Fatalf("同一 API key 因无关元数据变化而漂移：first=%q second=%q", first, second)
	}
	refresh := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeRefreshToken,
		[]byte(`{"refresh_token":"refresh-only"}`))
	if refresh == "" {
		t.Fatal("仅 refresh token 的启动凭据应生成稳定指纹")
	}
}

func TestCredentialMaterialFingerprintCoversStructuredCloudCredentials(t *testing.T) {
	awsA := []byte(`{"aws_access_key_id":"AKIA-A","aws_secret_access_key":"secret","aws_session_token":"session","aws_region":"us-east-1"}`)
	awsB := []byte(`{"aws_region":"eu-west-1","aws_session_token":"rotated-session","aws_secret_access_key":"secret","aws_access_key_id":"AKIA-A"}`)
	if first, second := CredentialMaterialFingerprint(7, VendorAnthropic, AuthModeBedrock, awsA),
		CredentialMaterialFingerprint(7, VendorAnthropic, AuthModeBedrock, awsB); first == "" || first != second {
		t.Fatalf("同一 AWS 长期凭据因 region 或临时 session 变化而漂移：first=%q second=%q", first, second)
	}
	if first, second := CredentialMaterialFingerprint(7, VendorAnthropic, AuthModeBedrock, awsA),
		CredentialMaterialFingerprint(7, VendorAnthropic, AuthModeBedrock,
			[]byte(`{"aws_access_key_id":"AKIA-B","aws_secret_access_key":"secret","aws_session_token":"session","aws_region":"us-east-1"}`)); first == second {
		t.Fatalf("不同 AWS access key id 不得共用指纹：%q", first)
	}

	serviceA := []byte(`{"client_email":"svc@example.test","private_key":"private","project_id":"one","token_uri":"https://oauth2.googleapis.com/token","access_token":"short-a","label":"one"}`)
	serviceB := []byte(`{"label":"two","access_token":"short-b","token_uri":"https://oauth2.googleapis.com/token","project_id":"one","private_key":"private","client_email":"svc@example.test"}`)
	if first, second := CredentialMaterialFingerprint(7, VendorGemini, AuthModeVertexSA, serviceA),
		CredentialMaterialFingerprint(7, VendorGemini, AuthModeVertexSA, serviceB); first == "" || first != second {
		t.Fatalf("同一服务账号因短期 token、JSON 顺序或标签变化而漂移：first=%q second=%q", first, second)
	}

	metadataA := []byte(`{"client_email":"runtime@example.test","metadata_token_endpoint":"http://metadata/token","project_id":"one","access_token":"short-a"}`)
	metadataB := []byte(`{"project_id":"one","access_token":"short-b","metadata_token_endpoint":"http://metadata/token","client_email":"runtime@example.test"}`)
	if first, second := CredentialMaterialFingerprint(7, VendorGemini, AuthModeVertexSA, metadataA),
		CredentialMaterialFingerprint(7, VendorGemini, AuthModeVertexSA, metadataB); first == "" || first != second {
		t.Fatalf("同一 metadata 身份因短期 token 变化而漂移：first=%q second=%q", first, second)
	}

	ordinaryOAuthA := []byte(`{"client_email":"display@example.test","access_token":"short-a","refresh_token":"refresh-a"}`)
	ordinaryOAuthB := []byte(`{"client_email":"display@example.test","access_token":"short-b","refresh_token":"refresh-b"}`)
	if first, second := CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, ordinaryOAuthA),
		CredentialMaterialFingerprint(7, VendorOpenAI, AuthModeCodexCLIOAuth, ordinaryOAuthB); first == "" || second == "" || first == second {
		t.Fatalf("普通 OAuth 的展示邮箱不得覆盖真实凭据材料：first=%q second=%q", first, second)
	}
}

func TestCredentialMaterialFingerprintRejectsInvalidScopeAndPayload(t *testing.T) {
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
