package accountbundle

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func TestEnvelopeRoundTripDoesNotExposePlaintext(t *testing.T) {
	secret := "provider-secret-material"
	content := payloadContent{
		CreatedAt: time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC),
		Accounts: []PortableAccount{{
			Ref: "account-a", SourceProviderID: 2, SourceChannelID: 3,
			Config:     PublicConfig{Name: "账号一", AccountType: "api_key", Enabled: true},
			Credential: PortableCredential{Vendor: "openai", AuthMode: "api_key", Payload: json.RawMessage(`{"api_key":"` + secret + `"}`)},
		}},
	}
	envelope, err := seal(content, "correct horse battery staple")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("加密迁移包响应泄露了凭据原文")
	}
	opened, err := open(envelope, "correct horse battery staple")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer zeroPortableContent(&opened)
	if got := string(opened.Accounts[0].Credential.Payload); !strings.Contains(got, secret) {
		t.Fatalf("round trip payload=%s", got)
	}
}

func TestEnvelopeRejectsTamperWrongPasswordAndKDFMutation(t *testing.T) {
	content := payloadContent{CreatedAt: time.Now().UTC(), Accounts: []PortableAccount{{
		Ref: "account-a", Credential: PortableCredential{Payload: json.RawMessage(`{"api_key":"secret"}`)},
	}}}
	envelope, err := seal(content, "correct horse battery staple")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	tampered := envelope
	tampered.Cipher.Ciphertext = append([]byte(nil), envelope.Cipher.Ciphertext...)
	tampered.Cipher.Ciphertext[0] ^= 0xff
	if _, err := open(tampered, "correct horse battery staple"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered err=%v want integrity", err)
	}
	if _, err := open(envelope, "incorrect password value"); !errors.Is(err, ErrPassword) {
		t.Fatalf("wrong password err=%v want password", err)
	}

	unsafeKDF := envelope
	unsafeKDF.KDF.MemoryKiB = 1024 * 1024
	if _, err := open(unsafeKDF, "correct horse battery staple"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("mutated kdf err=%v want integrity", err)
	}
}

func TestEnvelopeRejectsWeakPassword(t *testing.T) {
	_, err := seal(payloadContent{}, "too-short")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want invalid input", err)
	}
	bytes := []byte("sensitive")
	privacy.Zeroize(bytes)
	for _, value := range bytes {
		if value != 0 {
			t.Fatal("测试夹具清零失败")
		}
	}
}

func TestValidateOperatorAcceptsBothAdminRolesAndRejectsInvalidIdentity(t *testing.T) {
	for _, input := range []struct {
		tenantID int64
		actorID  string
		role     string
	}{
		{tenantID: 0, actorID: "admin_token:9", role: "tenant_operator"},
		{tenantID: 7, actorID: "", role: "tenant_operator"},
		{tenantID: 7, actorID: "admin_token:9", role: "end_user"},
	} {
		if err := validateOperator(input.tenantID, input.actorID, input.role); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("输入=%+v err=%v，期望拒绝", input, err)
		}
	}
	if err := validateOperator(7, "admin_token:9", "tenant_operator"); err != nil {
		t.Fatalf("租户操作者被误拒绝：%v", err)
	}
	if err := validateOperator(7, "admin_token:9", "platform_admin"); err != nil {
		t.Fatalf("部署者处理平台自有租户迁移包被误拒绝：%v", err)
	}
}
