// Package sign 实现 HUAKAI 信任链 T2：ed25519 签名 + verify + pubkey fingerprint。
//
// 用 stdlib `crypto/ed25519`，无外部依赖。
//
// 用途（trust-chain plan §3-§5）：
//   - Forwarder 在 stream 结束时对 Accounting.HopChain bytes 签名，写入
//     Accounting.Signature + Accounting.PubkeyFingerprint。
//   - Audit ledger（T4）每条记录用同样 keypair 签名。
//   - user-facing verify endpoint（T5）用 PubkeyFingerprint 在公开
//     `/.well-known/huakai-pubkey.json` 索引到对应公钥，验签。
//
// Why ed25519 not RSA / ECDSA：
//   - 64-byte signature（短，HTTP header 不臃肿）
//   - constant-time（侧信道安全）
//   - 性能：~70μs sign，~200μs verify（per stdlib bench）
//   - 业界共识（Sigstore / age / ssh 都用）
package sign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// PubkeyFingerprintLen 是公钥指纹的 hex 长度（sha256[:8] = 16 hex chars）。
// 选 16 chars 是 dashboard / response header 友好的紧凑长度。
const PubkeyFingerprintLen = 16

// Signer 用 ed25519 私钥对任意 bytes 签名。
// 零值不可用；必须用 NewSigner / NewSignerFromKey 构造。
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	fp   string
}

// ErrInvalidPrivateKey 私钥长度或格式不对。
var ErrInvalidPrivateKey = errors.New("sign: invalid ed25519 private key")

// NewSignerFromKey 用已有 ed25519 私钥构造 Signer。私钥长度必须是
// ed25519.PrivateKeySize (64 bytes)。
func NewSignerFromKey(priv ed25519.PrivateKey) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, ErrInvalidPrivateKey
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return nil, ErrInvalidPrivateKey
	}
	return &Signer{
		priv: priv,
		pub:  pub,
		fp:   Fingerprint(pub),
	}, nil
}

// Sign 对 message 做 ed25519 签名；输出固定 64 bytes。
func (s *Signer) Sign(message []byte) []byte {
	return ed25519.Sign(s.priv, message)
}

// PublicKey 返回对应公钥的浅拷贝；调用方不应修改。
func (s *Signer) PublicKey() ed25519.PublicKey {
	out := make(ed25519.PublicKey, len(s.pub))
	copy(out, s.pub)
	return out
}

// Fingerprint 返回公钥的紧凑指纹：sha256(pubkey)[:8] 的 hex（16 chars）。
// 与 NIST SP 800-185 风格一致，足够防碰撞用于 dashboard 显示 / 索引。
func (s *Signer) Fingerprint() string {
	return s.fp
}

// Fingerprint 对任意 ed25519 公钥计算指纹 sha256[:8] hex。
// 顶层 helper：测试 / verify 路径用，不需要 Signer 实例。
func Fingerprint(pub ed25519.PublicKey) string {
	if len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}
