package hermes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	EncryptedMessageContentPlaceholder = `{"encrypted":true}`

	hermesMessageContentVendor = "huakai_hermes"
	hermesMessageContentMode   = "message_content"
)

type encryptedMessageContent struct {
	Ciphertext       []byte `json:"ciphertext"`
	Nonce            []byte `json:"nonce"`
	KeyID            string `json:"key_id"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func EncodeMessageContent(ctx context.Context, keys credentialstore.KeyProvider, tenantID, conversationID int64, plaintext []byte) ([]byte, error) {
	if tenantID <= 0 || conversationID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id and conversation_id must be positive", ErrInvalidInput)
	}
	env, err := credentialstore.NewCipher(keys).Encrypt(ctx, plaintext, hermesMessageContentAAD(tenantID, conversationID))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(encryptedMessageContent{
		Ciphertext:       env.Ciphertext,
		Nonce:            env.Nonce,
		KeyID:            env.KeyID,
		EncryptionScheme: env.EncryptionScheme,
		AADHash:          env.AADHash,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal hermes message content envelope: %w", err)
	}
	return raw, nil
}

func DecodeMessageContent(ctx context.Context, keys credentialstore.KeyProvider, tenantID, conversationID int64, stored []byte) ([]byte, error) {
	if tenantID <= 0 || conversationID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id and conversation_id must be positive", ErrInvalidInput)
	}
	if len(stored) == 0 {
		return nil, nil
	}
	var packed encryptedMessageContent
	if err := json.Unmarshal(stored, &packed); err != nil {
		return nil, fmt.Errorf("%w: hermes message content envelope invalid", credentialstore.ErrDecryptFailed)
	}
	plain, err := credentialstore.NewCipher(keys).Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       packed.Ciphertext,
		Nonce:            packed.Nonce,
		KeyID:            packed.KeyID,
		EncryptionScheme: packed.EncryptionScheme,
		AADHash:          packed.AADHash,
	}, hermesMessageContentAAD(tenantID, conversationID))
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func hermesMessageContentAAD(tenantID, conversationID int64) credentialstore.AAD {
	// 复用 credentialstore.AAD 的 provider_account 槽位绑定 conversation_id,避免为 Hermes 另造加密信封。
	return credentialstore.AAD{
		TenantID:          tenantID,
		ProviderAccountID: conversationID,
		Vendor:            hermesMessageContentVendor,
		AuthMode:          hermesMessageContentMode,
		Version:           1,
	}
}
