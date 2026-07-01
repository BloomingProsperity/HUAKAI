package platformsettings

import (
	"context"
	"strings"
)

// SecretCipher 对 secret 设置值做 at-rest 加密/解密。string-in-string-out,使 platformsettings 与具体
// 加密实现(credentialstore.Cipher/AES-GCM)解耦——由 wiring 用一个适配器包装注入,处理 Envelope 序列化。
// aad(附加认证数据)= setting key,把密文绑定到具体 key,防止密文被搬到别的 key 复用。
type SecretCipher interface {
	EncryptString(ctx context.Context, plaintext, aad string) (string, error)
	DecryptString(ctx context.Context, ciphertext, aad string) (string, error)
}

// secretEncPrefix 是已加密 secret 值的版本前缀。读路径据此区分「已加密(须解密)」与「存量明文
// (未加密,直接用)」,实现平滑迁移:老的明文 secret 无前缀、按明文读;新写入的 secret 带前缀、解密读。
const secretEncPrefix = "encv1:"

// WithSecretCipher 注入 secret 加密器。未注入时 secret 值按明文存/读(与本次改动前行为一致)。
func WithSecretCipher(cipher SecretCipher) Option {
	return func(s *Service) {
		s.secretCipher = cipher
	}
}

// encryptSecretValue 在写入前加密 secret-key 的非空值(打上版本前缀)。非 secret key / 未注入 cipher /
// 空值 / 已带前缀(幂等)一律原样返回。
func (s *Service) encryptSecretValue(ctx context.Context, key SettingKey, value string) (string, error) {
	if s == nil || s.secretCipher == nil || !IsSecretKey(key) {
		return value, nil
	}
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, secretEncPrefix) {
		return value, nil
	}
	enc, err := s.secretCipher.EncryptString(ctx, value, string(key))
	if err != nil {
		return "", err
	}
	return secretEncPrefix + enc, nil
}

// decryptSecretValue 在读出后解密带前缀的 secret 值(供 server 侧消费方拿明文)。无前缀(存量明文)或
// 非 secret key 直接返回;解密失败返回错误(由调用方 fail-safe 到 lastKnown/default,不静默返回密文)。
func (s *Service) decryptSecretValue(ctx context.Context, key SettingKey, value string) (string, error) {
	if s == nil || s.secretCipher == nil || !IsSecretKey(key) {
		return value, nil
	}
	if !strings.HasPrefix(value, secretEncPrefix) {
		return value, nil
	}
	plain, err := s.secretCipher.DecryptString(ctx, strings.TrimPrefix(value, secretEncPrefix), string(key))
	if err != nil {
		return "", err
	}
	return plain, nil
}
