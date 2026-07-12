package platformsettings

import (
	"context"
	"strings"
	"testing"
)

// fakeSecretCipher 是可逆的测试用密码器:密文 = "C(" + aad + ")" + plaintext,解密时校验 aad 并剥壳。
// 它足以验证「加密后落库、读出解密、aad 绑定 key」的往返正确性(不依赖真 AES)。
type fakeSecretCipher struct{}

func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func (fakeSecretCipher) EncryptString(_ context.Context, plaintext, aad string) (string, error) {
	// 反转明文以隐藏子串(模拟真加密的「密文不含明文」性质),使「无明文泄露」断言有意义。
	return "C(" + aad + ")" + reverseString(plaintext), nil
}
func (fakeSecretCipher) DecryptString(_ context.Context, ciphertext, aad string) (string, error) {
	prefix := "C(" + aad + ")"
	if !strings.HasPrefix(ciphertext, prefix) {
		return "", ErrInvalidValue // aad 不匹配(密文被搬到别的 key)→ 拒绝
	}
	return reverseString(strings.TrimPrefix(ciphertext, prefix)), nil
}

// TestSecretSettingEncryptedAtRest 锁定 secret 设置的 at-rest 加密往返:
// ① Upsert 后库里存的是带前缀的密文(非明文);② Get 读出解密回明文(server 消费方拿明文);
// ③ 非 secret key 不受影响(明文进出);④ aad 绑定 key(密文含 key)。
// 变异:Upsert 不加密(encryptSecretValue 直接返回 value)→ 库里存明文,断言①(库存密文)RED。
func TestSecretSettingEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))

	// moderation_external_api_keys 是既有 secret key(JSON 字符串数组)。
	const secretKey = KeyModerationExternalAPIKeys
	const plaintext = `["sk-super-secret"]`
	if _, err := svc.Upsert(ctx, UpsertInput{Key: secretKey, Value: plaintext, UpdatedBy: "t"}); err != nil {
		t.Fatalf("upsert secret: %v", err)
	}

	// ① 库里(绕过 service)存的必须是带前缀的密文,不是明文。
	raw, found, err := store.Get(ctx, GlobalScope, string(secretKey))
	if err != nil || !found {
		t.Fatalf("store.Get: found=%v err=%v", found, err)
	}
	if !strings.HasPrefix(raw.Value, secretEncPrefix) {
		t.Fatalf("库里 secret 应为带前缀密文,实为 %q", raw.Value)
	}
	if strings.Contains(raw.Value, "sk-super-secret") {
		t.Fatalf("库里 secret 明文泄露:%q", raw.Value)
	}
	// ② 密文里应含 aad(=key),证明绑定。
	if !strings.Contains(raw.Value, string(secretKey)) {
		t.Fatalf("密文应绑定 key(aad),实为 %q", raw.Value)
	}

	// ③ Get 读出解密回明文(server 消费方语义)。
	got, err := svc.Get(ctx, secretKey)
	if err != nil {
		t.Fatalf("svc.Get: %v", err)
	}
	if got.Value != plaintext {
		t.Fatalf("Get 应解密回明文 %q,实得 %q", plaintext, got.Value)
	}

	// ④ 非 secret key 不加密(明文进出)。
	if _, err := svc.Upsert(ctx, UpsertInput{Key: KeySiteName, Value: "MySite", UpdatedBy: "t"}); err != nil {
		t.Fatalf("upsert non-secret: %v", err)
	}
	rawName, _, _ := store.Get(ctx, GlobalScope, string(KeySiteName))
	if rawName.Value != "MySite" {
		t.Fatalf("非 secret key 不应加密,库里应为明文 MySite,实得 %q", rawName.Value)
	}
}

// TestSecretSettingPlaintextCompat 锁定存量明文兼容:未加密(无前缀)的旧 secret 值按明文读,不报错。
func TestSecretSettingPlaintextCompat(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	// 直接往库里塞一条无前缀的明文 secret(模拟存量/env 迁移前)。
	if _, err := store.Upsert(ctx, GlobalScope, string(KeyModerationExternalAPIKeys), `["legacy-plain"]`, "t"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))
	got, err := svc.Get(ctx, KeyModerationExternalAPIKeys)
	if err != nil {
		t.Fatalf("Get legacy plaintext: %v", err)
	}
	if got.Value != `["legacy-plain"]` {
		t.Fatalf("存量明文应原样读出,实得 %q", got.Value)
	}
}
