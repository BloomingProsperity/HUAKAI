package sign

import (
	"crypto/ed25519"
	"errors"
)

// ErrSignatureMismatch verify 失败时返回。
var ErrSignatureMismatch = errors.New("sign: signature does not match message under this public key")

// ErrInvalidPublicKey 公钥长度或格式不对。
var ErrInvalidPublicKey = errors.New("sign: invalid ed25519 public key")

// ErrInvalidSignature 签名长度不对（必须 64 bytes）。
var ErrInvalidSignature = errors.New("sign: invalid ed25519 signature length")

// Verify 用公钥校验 message 的签名。返回 nil 表示 verify 通过；其它错误表示
// 不通过或格式问题。Verify 是 user-facing CLI 的核心入口（T5）。
func Verify(pub ed25519.PublicKey, message, signature []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(pub, message, signature) {
		return ErrSignatureMismatch
	}
	return nil
}
