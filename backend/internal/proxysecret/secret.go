package proxysecret

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const EnvelopePrefix = "huakai-proxy-secret-v1:"

type encryptedSecret struct {
	Ciphertext       []byte `json:"ciphertext"`
	Nonce            []byte `json:"nonce"`
	KeyID            string `json:"key_id"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func Encode(ctx context.Context, keys credentialstore.KeyProvider, tenantID int64, plaintext string) (string, error) {
	cipher := credentialstore.NewCipher(keys)
	env, err := cipher.Encrypt(ctx, []byte(plaintext), proxySecretAAD(tenantID))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(encryptedSecret{
		Ciphertext:       env.Ciphertext,
		Nonce:            env.Nonce,
		KeyID:            env.KeyID,
		EncryptionScheme: env.EncryptionScheme,
		AADHash:          env.AADHash,
	})
	if err != nil {
		return "", err
	}
	return EnvelopePrefix + string(raw), nil
}

func Decode(ctx context.Context, keys credentialstore.KeyProvider, tenantID int64, stored string) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, EnvelopePrefix) {
		return "", fmt.Errorf("%w: proxy auth_secret envelope missing", credentialstore.ErrDecryptFailed)
	}
	var packed encryptedSecret
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stored, EnvelopePrefix)), &packed); err != nil {
		return "", fmt.Errorf("%w: proxy auth_secret envelope invalid", credentialstore.ErrDecryptFailed)
	}
	cipher := credentialstore.NewCipher(keys)
	plaintext, err := cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       packed.Ciphertext,
		Nonce:            packed.Nonce,
		KeyID:            packed.KeyID,
		EncryptionScheme: packed.EncryptionScheme,
		AADHash:          packed.AADHash,
	}, proxySecretAAD(tenantID))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func proxySecretAAD(tenantID int64) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID:          tenantID,
		ProviderAccountID: 0,
		Vendor:            "huakai_forward_proxy",
		AuthMode:          "proxy_auth_secret",
		Version:           1,
	}
}
