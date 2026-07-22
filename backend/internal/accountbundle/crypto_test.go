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
		scope    int64
		actorID  string
		role     string
	}{
		{tenantID: 0, scope: 0, actorID: "admin_token:9", role: "tenant_operator"},
		{tenantID: 7, scope: 7, actorID: "", role: "tenant_operator"},
		{tenantID: 7, scope: 7, actorID: "admin_token:9", role: "end_user"},
	} {
		if err := validateOperator(input.tenantID, input.scope, input.actorID, input.role); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("输入=%+v err=%v，期望拒绝(输入无效)", input, err)
		}
	}
	// 纵深第二锁的判别性用例:身份合法但自有租户与目标租户不符 → 必须 ErrForbidden。
	// 变异:删掉 validateOperator 里 actorScopeTenantID 断言,以下三例转红。
	for _, input := range []struct {
		tenantID int64
		scope    int64
		role     string
	}{
		{tenantID: 7, scope: 8, role: "tenant_operator"}, // 租户管理员越权碰别的租户
		{tenantID: 7, scope: 0, role: "tenant_operator"}, // 自有租户缺失
		{tenantID: 7, scope: 9, role: "platform_admin"},  // 部署者 scope(已重绑)与目标不符
	} {
		if err := validateOperator(input.tenantID, input.scope, "admin_token:9", input.role); !errors.Is(err, ErrForbidden) {
			t.Fatalf("输入=%+v err=%v，期望 ErrForbidden(跨租户越权)", input, err)
		}
	}
	if err := validateOperator(7, 7, "admin_token:9", "tenant_operator"); err != nil {
		t.Fatalf("租户操作者处理自有租户被误拒绝：%v", err)
	}
	if err := validateOperator(7, 7, "admin_token:9", "platform_admin"); err != nil {
		t.Fatalf("部署者处理平台自有租户迁移包被误拒绝：%v", err)
	}
}
