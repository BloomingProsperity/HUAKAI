package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// GenerateKey 用 crypto/rand 生成新 ed25519 keypair，返回构造好的 Signer。
// 仅用于 dev / 启动期初次生成；生产环境 priv key 应从 KMS / vault / env var
// 加载（参考 trust-chain plan Q2 决策点）。
func GenerateKey() (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sign: GenerateKey: %w", err)
	}
	return NewSignerFromKey(priv)
}
