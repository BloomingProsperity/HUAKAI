package email

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const secretEnvelopePrefix = "huakai-email-secret-v1:"

type SecretKeyProvider = credentialstore.KeyProvider

type StoredSettings map[string]string

type StoredSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

type SettingsStore interface {
	Load(context.Context, int64) (StoredSettings, error)
	List(context.Context, int64) ([]StoredSetting, error)
	Save(context.Context, int64, map[string]string, string) error
	ListActiveTenantIDs(context.Context) ([]int64, error)
}

type PostgresSettingsStore struct {
	db db.DBTX
}

func NewPostgresSettingsStore(database db.DBTX) *PostgresSettingsStore {
	return &PostgresSettingsStore{db: database}
}

func (s *PostgresSettingsStore) Load(ctx context.Context, tenantID int64) (StoredSettings, error) {
	rows, err := s.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make(StoredSettings, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

func (s *PostgresSettingsStore) List(ctx context.Context, tenantID int64) ([]StoredSetting, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: settings store unavailable", ErrEmailBackendUnconfigured)
	}
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id", ErrEmailSettingsInvalid)
	}
	const q = `
SELECT setting_key, setting_value, updated_at, updated_by
FROM email_settings
WHERE tenant_id = $1
ORDER BY setting_key`
	rows, err := s.db.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredSetting
	for rows.Next() {
		var row StoredSetting
		if err := rows.Scan(&row.Key, &row.Value, &row.UpdatedAt, &row.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *PostgresSettingsStore) Save(ctx context.Context, tenantID int64, values map[string]string, actor string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: settings store unavailable", ErrEmailBackendUnconfigured)
	}
	if tenantID <= 0 {
		return fmt.Errorf("%w: tenant_id", ErrEmailSettingsInvalid)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "system"
	}
	const q = `
INSERT INTO email_settings (tenant_id, setting_key, setting_value, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, setting_key)
DO UPDATE SET setting_value = EXCLUDED.setting_value,
              updated_by = EXCLUDED.updated_by,
              updated_at = now()`
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := s.db.Exec(ctx, q, tenantID, key, value, actor); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresSettingsStore) ListActiveTenantIDs(ctx context.Context) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: settings store unavailable", ErrEmailBackendUnconfigured)
	}
	// id > 0 排除非正 id 的系统伪租户:迁移 0030 用显式 id=0 播种了
	// 'public-pricing' 全局定价 scope 哨兵(状态 active),它没有 users、永不发验证邮件,
	// 且配置侧对 tenant_id<=0 一律拒写。生产 email 就绪门遍历本列表逐租户要求配齐 SMTP,
	// 若把该哨兵纳入,会要求一个任何配置入口都拒绝写入的租户配齐 SMTP → 生产永久拒启。
	// 收窄到正 id 工作租户(与工作租户既定口径一致),使"门要求配置的集合"恰等于
	// "配置入口能写入的集合",消解该死锁;不改哨兵那行、不影响公开定价 scope。
	const q = `
SELECT id
FROM tenants
WHERE status = 'active' AND deleted_at IS NULL AND id > 0
ORDER BY id`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type encryptedSecret struct {
	Ciphertext       []byte `json:"ciphertext"`
	Nonce            []byte `json:"nonce"`
	KeyID            string `json:"key_id"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func EncodeSecret(ctx context.Context, keys SecretKeyProvider, tenantID int64, plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", fmt.Errorf("%w: empty smtp password", ErrEmailSettingsInvalid)
	}
	cipher := credentialstore.NewCipher(keys)
	env, err := cipher.Encrypt(ctx, []byte(plaintext), emailSecretAAD(tenantID))
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
	return secretEnvelopePrefix + string(raw), nil
}

func DecodeSecret(ctx context.Context, keys SecretKeyProvider, tenantID int64, stored string) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, secretEnvelopePrefix) {
		return "", fmt.Errorf("%w: smtp password envelope missing", credentialstore.ErrDecryptFailed)
	}
	var packed encryptedSecret
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stored, secretEnvelopePrefix)), &packed); err != nil {
		return "", fmt.Errorf("%w: smtp password envelope invalid", credentialstore.ErrDecryptFailed)
	}
	cipher := credentialstore.NewCipher(keys)
	plaintext, err := cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       packed.Ciphertext,
		Nonce:            packed.Nonce,
		KeyID:            packed.KeyID,
		EncryptionScheme: packed.EncryptionScheme,
		AADHash:          packed.AADHash,
	}, emailSecretAAD(tenantID))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func emailSecretAAD(tenantID int64) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID:          tenantID,
		ProviderAccountID: 0,
		Vendor:            "huakai_email_delivery",
		AuthMode:          "smtp_password",
		Version:           1,
	}
}
