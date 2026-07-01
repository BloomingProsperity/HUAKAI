// Package settingscipher 把 credentialstore 的 AES-GCM 密码器适配成 platformsettings.SecretCipher
// (string-in-string-out),用于 secret 平台设置的 at-rest 加密。放在独立小包,让 platformsettings 与
// credentialstore 保持解耦。
package settingscipher

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// Adapter 实现 platformsettings.SecretCipher。把明文经 credentialstore.Cipher 加密成 Envelope,
// 再 JSON+base64 序列化成一个不透明字符串存进设置值;解密反之。
type Adapter struct {
	cipher *credentialstore.Cipher
}

// New 用一个已构造的 credentialstore.Cipher 建适配器。cipher 为 nil 返回 nil(调用方据此不注入,
// 设置退回明文存储)。
func New(cipher *credentialstore.Cipher) *Adapter {
	if cipher == nil {
		return nil
	}
	return &Adapter{cipher: cipher}
}

// aadFor 把 setting key(平台设置的 aad)映射进 credentialstore.AAD。Vendor 固定标识本用途、
// AuthMode 承载 setting key,使密文绑定到具体 key(密文被搬到别的 key 时解密因 aad 不符而失败)。
func aadFor(settingKey string) credentialstore.AAD {
	return credentialstore.AAD{Vendor: "platform_setting", AuthMode: settingKey}
}

func (a *Adapter) EncryptString(ctx context.Context, plaintext, aad string) (string, error) {
	env, err := a.cipher.Encrypt(ctx, []byte(plaintext), aadFor(aad))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func (a *Adapter) DecryptString(ctx context.Context, ciphertext, aad string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	var env credentialstore.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	plain, err := a.cipher.Decrypt(ctx, env, aadFor(aad))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
