package settingscipher

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// TestAdapterRoundTrip 用真 AES-GCM 验证:加密→序列化→反序列化→解密回明文;且 aad(setting key)绑定——
// 换 key 解密失败。变异:aadFor 忽略 settingKey(恒定 aad)→ 换 key 也能解 → aad 断言 RED。
func TestAdapterRoundTrip(t *testing.T) {
	ctx := context.Background()
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("rand: %v", err)
	}
	kp, err := credentialstore.NewStaticKeyProvider("k1", material)
	if err != nil {
		t.Fatalf("keyprovider: %v", err)
	}
	a := New(credentialstore.NewCipher(kp))
	if a == nil {
		t.Fatal("New 返回 nil")
	}

	const plaintext = `["sk-super-secret"]`
	enc, err := a.EncryptString(ctx, plaintext, "telegram_bot_token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains([]byte(enc), []byte("sk-super-secret")) {
		t.Fatalf("密文含明文子串:%s", enc)
	}
	dec, err := a.DecryptString(ctx, enc, "telegram_bot_token")
	if err != nil || dec != plaintext {
		t.Fatalf("解密回明文失败:dec=%q err=%v", dec, err)
	}
	// aad 绑定:用不同 key 解密应失败。
	if _, err := a.DecryptString(ctx, enc, "other_key"); err == nil {
		t.Fatal("用不同 aad(key)解密应失败(aad 未绑定?)")
	}
}
