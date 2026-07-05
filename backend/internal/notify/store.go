package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

const notifySecretEnvelopePrefix = "huakai-notify-secret-v1:"

type sqlDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db   sqlDB
	keys credentialstore.KeyProvider
}

func NewPostgresStore(database sqlDB, keys credentialstore.KeyProvider) *PostgresStore {
	return &PostgresStore{db: database, keys: keys}
}

func (s *PostgresStore) GetSettings(ctx context.Context, tenantID, userID int64) (Settings, error) {
	if s == nil || s.db == nil {
		return Settings{}, ErrStoreUnavailable
	}
	if tenantID <= 0 || userID <= 0 {
		return Settings{}, fmt.Errorf("%w: tenant_id/user_id", ErrInvalidSettings)
	}
	const q = `
SELECT notify_type,
       webhook_url,
       webhook_secret,
       notification_email,
       bark_url,
       gotify_url,
       gotify_token,
       gotify_priority,
       balance_threshold::text,
       updated_at,
       updated_by,
       threshold_type,
       extra_emails
FROM user_notification_settings
WHERE tenant_id = $1 AND user_id = $2`
	var (
		out            = DefaultSettings(tenantID, userID)
		thresholdRaw   string
		extraEmailsRaw string
	)
	err := s.db.QueryRow(ctx, q, tenantID, userID).Scan(
		&out.NotifyType,
		&out.WebhookURL,
		&out.WebhookSecret,
		&out.NotificationEmail,
		&out.BarkURL,
		&out.GotifyURL,
		&out.GotifyToken,
		&out.GotifyPriority,
		&thresholdRaw,
		&out.UpdatedAt,
		&out.UpdatedBy,
		&out.ThresholdType,
		&extraEmailsRaw,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return out, nil
		}
		return Settings{}, err
	}
	out.ExtraEmails = decodeExtraEmails(extraEmailsRaw)
	return s.finishLoadedSettings(ctx, out, thresholdRaw)
}

func (s *PostgresStore) ListActiveSettings(ctx context.Context, tenantID int64) ([]Settings, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreUnavailable
	}
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id", ErrInvalidSettings)
	}
	const q = `
	SELECT uns.user_id,
	       uns.notify_type,
	       uns.webhook_url,
	       uns.webhook_secret,
	       uns.notification_email,
	       uns.bark_url,
	       uns.gotify_url,
	       uns.gotify_token,
	       uns.gotify_priority,
	       uns.balance_threshold::text,
	       uns.updated_at,
	       uns.updated_by,
	       uns.threshold_type,
	       uns.extra_emails
	FROM user_notification_settings uns
	JOIN users ON users.tenant_id = uns.tenant_id
	          AND users.id = uns.user_id
	          AND users.role = 'admin'
	          AND users.deleted_at IS NULL
	WHERE uns.tenant_id = $1
	  AND uns.notify_type <> 'none'
	ORDER BY uns.user_id ASC`
	rows, err := s.db.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Settings{}
	for rows.Next() {
		settings := DefaultSettings(tenantID, 1)
		settings.TenantID = tenantID
		var thresholdRaw, extraEmailsRaw string
		if err := rows.Scan(
			&settings.UserID,
			&settings.NotifyType,
			&settings.WebhookURL,
			&settings.WebhookSecret,
			&settings.NotificationEmail,
			&settings.BarkURL,
			&settings.GotifyURL,
			&settings.GotifyToken,
			&settings.GotifyPriority,
			&thresholdRaw,
			&settings.UpdatedAt,
			&settings.UpdatedBy,
			&settings.ThresholdType,
			&extraEmailsRaw,
		); err != nil {
			return nil, err
		}
		settings.ExtraEmails = decodeExtraEmails(extraEmailsRaw)
		settings, err = s.finishLoadedSettings(ctx, settings, thresholdRaw)
		if err != nil {
			return nil, err
		}
		out = append(out, settings)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) finishLoadedSettings(ctx context.Context, out Settings, thresholdRaw string) (Settings, error) {
	threshold, err := decimal.NewFromString(thresholdRaw)
	if err != nil {
		return Settings{}, fmt.Errorf("%w: balance_threshold", ErrInvalidSettings)
	}
	out.BalanceThreshold = threshold
	if out.WebhookSecret, err = s.decodeSecret(ctx, out.TenantID, out.UserID, "webhook_secret", out.WebhookSecret); err != nil {
		return Settings{}, err
	}
	if out.GotifyToken, err = s.decodeSecret(ctx, out.TenantID, out.UserID, "gotify_token", out.GotifyToken); err != nil {
		return Settings{}, err
	}
	out = out.normalized()
	if out.NotifyType != TypeGotify {
		out.GotifyPriority = 0
	}
	return out, nil
}

func (s *PostgresStore) UpsertSettings(ctx context.Context, settings Settings) (Settings, error) {
	if s == nil || s.db == nil {
		return Settings{}, ErrStoreUnavailable
	}
	normalized, err := ValidateSettings(scrubInactiveFields(settings))
	if err != nil {
		return Settings{}, err
	}
	if normalized.NotifyType != TypeGotify {
		normalized.GotifyPriority = 0
	}
	storedSecret, err := s.encodeSecret(ctx, normalized.TenantID, normalized.UserID, "webhook_secret", normalized.WebhookSecret)
	if err != nil {
		return Settings{}, err
	}
	storedGotifyToken, err := s.encodeSecret(ctx, normalized.TenantID, normalized.UserID, "gotify_token", normalized.GotifyToken)
	if err != nil {
		return Settings{}, err
	}
	const q = `
INSERT INTO user_notification_settings (
	tenant_id,
	user_id,
	notify_type,
	webhook_url,
	webhook_secret,
	notification_email,
	bark_url,
	gotify_url,
	gotify_token,
	gotify_priority,
	balance_threshold,
	updated_by,
	threshold_type,
	extra_emails
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET notify_type = EXCLUDED.notify_type,
              webhook_url = EXCLUDED.webhook_url,
              webhook_secret = EXCLUDED.webhook_secret,
              notification_email = EXCLUDED.notification_email,
              bark_url = EXCLUDED.bark_url,
              gotify_url = EXCLUDED.gotify_url,
              gotify_token = EXCLUDED.gotify_token,
              gotify_priority = EXCLUDED.gotify_priority,
              balance_threshold = EXCLUDED.balance_threshold,
              threshold_type = EXCLUDED.threshold_type,
              extra_emails = EXCLUDED.extra_emails,
              updated_by = EXCLUDED.updated_by,
              updated_at = now()
RETURNING updated_at`
	if err := s.db.QueryRow(ctx, q,
		normalized.TenantID,
		normalized.UserID,
		normalized.NotifyType,
		normalized.WebhookURL,
		storedSecret,
		normalized.NotificationEmail,
		normalized.BarkURL,
		normalized.GotifyURL,
		storedGotifyToken,
		normalized.GotifyPriority,
		normalized.BalanceThreshold.StringFixedBank(8),
		normalized.UpdatedBy,
		normalized.ThresholdType,
		encodeExtraEmails(normalized.ExtraEmails),
	).Scan(&normalized.UpdatedAt); err != nil {
		return Settings{}, err
	}
	return normalized, nil
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) GetSettings(ctx context.Context, tenantID, userID int64) (Settings, error) {
	if s == nil || s.store == nil {
		return Settings{}, ErrStoreUnavailable
	}
	return s.store.GetSettings(ctx, tenantID, userID)
}

func (s *Service) UpsertSettings(ctx context.Context, settings Settings) (Settings, error) {
	if s == nil || s.store == nil {
		return Settings{}, ErrStoreUnavailable
	}
	return s.store.UpsertSettings(ctx, settings)
}

func scrubInactiveFields(settings Settings) Settings {
	s := settings.normalized()
	switch s.NotifyType {
	case TypeEmail:
		s.WebhookURL, s.WebhookSecret, s.BarkURL, s.GotifyURL, s.GotifyToken = "", "", "", "", ""
		s.GotifyPriority = 0
	case TypeWebhook:
		s.NotificationEmail, s.BarkURL, s.GotifyURL, s.GotifyToken = "", "", "", ""
		s.GotifyPriority = 0
	case TypeBark:
		s.WebhookURL, s.WebhookSecret, s.NotificationEmail, s.GotifyURL, s.GotifyToken = "", "", "", "", ""
		s.GotifyPriority = 0
	case TypeGotify:
		s.WebhookURL, s.WebhookSecret, s.NotificationEmail, s.BarkURL = "", "", "", ""
	default:
		s.WebhookURL, s.WebhookSecret, s.NotificationEmail, s.BarkURL, s.GotifyURL, s.GotifyToken = "", "", "", "", "", ""
		s.GotifyPriority = 0
	}
	return s
}

func (s *PostgresStore) encodeSecret(ctx context.Context, tenantID, userID int64, authMode, plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", nil
	}
	cipher := credentialstore.NewCipher(s.keys)
	env, err := cipher.Encrypt(ctx, []byte(plaintext), notifySecretAAD(tenantID, userID, authMode))
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
	return notifySecretEnvelopePrefix + string(raw), nil
}

func (s *PostgresStore) decodeSecret(ctx context.Context, tenantID, userID int64, authMode, stored string) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, notifySecretEnvelopePrefix) {
		return "", fmt.Errorf("%w: notify secret envelope missing", credentialstore.ErrDecryptFailed)
	}
	var packed encryptedSecret
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stored, notifySecretEnvelopePrefix)), &packed); err != nil {
		return "", fmt.Errorf("%w: notify secret envelope invalid", credentialstore.ErrDecryptFailed)
	}
	cipher := credentialstore.NewCipher(s.keys)
	plaintext, err := cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       packed.Ciphertext,
		Nonce:            packed.Nonce,
		KeyID:            packed.KeyID,
		EncryptionScheme: packed.EncryptionScheme,
		AADHash:          packed.AADHash,
	}, notifySecretAAD(tenantID, userID, authMode))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

type encryptedSecret struct {
	Ciphertext       []byte `json:"ciphertext"`
	Nonce            []byte `json:"nonce"`
	KeyID            string `json:"key_id"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func notifySecretAAD(tenantID, userID int64, authMode string) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID:          tenantID,
		ProviderAccountID: userID,
		Vendor:            "huakai_notify",
		AuthMode:          authMode,
		Version:           1,
	}
}

func encodeExtraEmails(emails []string) string {
	if len(emails) == 0 {
		return "[]"
	}
	b, err := json.Marshal(emails)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeExtraEmails(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
