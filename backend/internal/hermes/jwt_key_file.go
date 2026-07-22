package hermes

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// LoadPrivateKey 只接受权限为 0400 的 Ed25519 PKCS#8 私钥，避免运行器鉴权密钥被同组用户读取。
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat hermes jwt private key: %w", err)
	}
	if info.Mode().Perm() != 0o400 {
		return nil, fmt.Errorf("%w: jwt private key must be 0400", ErrMisconfigured)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hermes jwt private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%w: jwt private key must be PEM", ErrInvalidInput)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: parse jwt private key PEM: %v", ErrInvalidInput, err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: jwt private key must be Ed25519", ErrInvalidInput)
	}
	return privateKey, nil
}
