package credentialstore

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCodexAgentIdentityHandlerStrictlyValidatesPayload(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"runtime_id": "runtime-1", "private_key_pkcs8": base64.StdEncoding.EncodeToString(der),
		"upstream_account_id": "account-1", "upstream_user_id": "user-1", "fedramp": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := DefaultHandlerRegistry().MustLookup(VendorOpenAI, AuthModeCodexAgentIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if handler.RuntimeKind() != RuntimeCodexAgentIdentity {
		t.Fatalf("runtime kind=%q", handler.RuntimeKind())
	}
	if err := handler.ValidatePayload(payload); err != nil {
		t.Fatalf("合法凭据被拒绝: %v", err)
	}
	for name, raw := range map[string][]byte{
		"缺私钥":     []byte(`{"runtime_id":"r","upstream_account_id":"a","upstream_user_id":"u"}`),
		"伪造密钥":    []byte(`{"runtime_id":"r","private_key_pkcs8":"bm90LWtleQ==","upstream_account_id":"a","upstream_user_id":"u"}`),
		"未知字段":    append(payload[:len(payload)-1], []byte(`,"base_url":"https://attacker.invalid"}`)...),
		"错误布尔类型":  []byte(`{"runtime_id":"r","private_key_pkcs8":"` + base64.StdEncoding.EncodeToString(der) + `","upstream_account_id":"a","upstream_user_id":"u","fedramp":"false"}`),
		"运行时标识过大": []byte(`{"runtime_id":"` + strings.Repeat("x", 513) + `","private_key_pkcs8":"` + base64.StdEncoding.EncodeToString(der) + `","upstream_account_id":"a","upstream_user_id":"u"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := handler.ValidatePayload(raw); !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("err=%v want ErrInvalidPayload", err)
			}
		})
	}
}
