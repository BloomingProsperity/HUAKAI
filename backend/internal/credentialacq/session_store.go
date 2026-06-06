package credentialacq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const DefaultFlowTTL = 10 * time.Minute
const transientPayloadMetadataPrefix = "huakai-transient-payload-v1:"

type PostgresSessionStore struct {
	db     db.DBTX
	cipher *credentialstore.Cipher
	now    func() time.Time
}

type transientPayloadMetadata struct {
	KeyID            string `json:"key_id"`
	Nonce            []byte `json:"nonce"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func NewPostgresSessionStore(database db.DBTX) *PostgresSessionStore {
	return &PostgresSessionStore{db: database, now: time.Now}
}

func NewPostgresSessionStoreWithKeys(database db.DBTX, keys credentialstore.KeyProvider) *PostgresSessionStore {
	return NewPostgresSessionStore(database).WithKeyProvider(keys)
}

func (s *PostgresSessionStore) WithNow(now func() time.Time) *PostgresSessionStore {
	cp := *s
	if now != nil {
		cp.now = now
	}
	return &cp
}

func (s *PostgresSessionStore) WithKeyProvider(keys credentialstore.KeyProvider) *PostgresSessionStore {
	cp := *s
	if keys != nil {
		cp.cipher = credentialstore.NewCipher(keys)
	}
	return &cp
}

func (s *PostgresSessionStore) EncryptTransientPayload(ctx context.Context, plaintext []byte, aad credentialstore.AAD) ([]byte, []byte, string, error) {
	if s == nil || s.cipher == nil {
		return nil, nil, "", fmt.Errorf("%w: credential acquisition transient cipher not configured", credentialstore.ErrKeyUnavailable)
	}
	env, err := s.cipher.Encrypt(ctx, plaintext, aad)
	if err != nil {
		return nil, nil, "", err
	}
	metadata, err := json.Marshal(transientPayloadMetadata{
		KeyID:            env.KeyID,
		Nonce:            env.Nonce,
		EncryptionScheme: env.EncryptionScheme,
		AADHash:          env.AADHash,
	})
	if err != nil {
		return nil, nil, "", err
	}
	packed := append([]byte(transientPayloadMetadataPrefix), metadata...)
	return env.Ciphertext, packed, env.KeyID, nil
}

func (s *PostgresSessionStore) DecryptTransientPayload(ctx context.Context, ciphertext, metadata []byte, aad credentialstore.AAD) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	if s == nil || s.cipher == nil {
		return nil, fmt.Errorf("%w: credential acquisition transient cipher not configured", credentialstore.ErrKeyUnavailable)
	}
	if !bytes.HasPrefix(metadata, []byte(transientPayloadMetadataPrefix)) {
		return nil, fmt.Errorf("%w: transient payload metadata missing", credentialstore.ErrDecryptFailed)
	}
	var meta transientPayloadMetadata
	if err := json.Unmarshal(bytes.TrimPrefix(metadata, []byte(transientPayloadMetadataPrefix)), &meta); err != nil {
		return nil, fmt.Errorf("%w: transient payload metadata invalid", credentialstore.ErrDecryptFailed)
	}
	return s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       ciphertext,
		Nonce:            meta.Nonce,
		KeyID:            meta.KeyID,
		EncryptionScheme: meta.EncryptionScheme,
		AADHash:          meta.AADHash,
	}, aad)
}

func (s *PostgresSessionStore) CreateFromStart(ctx context.Context, in StartInput) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	in.Vendor = credentialstore.Normalize(in.Vendor)
	in.AuthMode = credentialstore.Normalize(in.AuthMode)
	plan, ok := LookupModePlan(in.Vendor, in.AuthMode)
	if !ok {
		return Session{}, ErrUnknownMode
	}
	if in.Kind == "" {
		in.Kind = plan.Kind
	}
	in.Kind = NormalizeFlowKind(in.Kind)
	if in.Kind == "" {
		return Session{}, ErrInvalidImportBody
	}
	// 闸门: 不能让 caller 用任意 flow_kind 绕开
	// ModePlan.AllowedHelpers。例如 chatgpt_oauth / code_assist / google_one
	// 这种 OAuth-only mode 不应被 POST /admin/v1/credentials/paste 手工
	// finalize 替代;CreateFromStart 是 trust 根, 所有调用者 (admin handler /
	// future API caller) 都要被这层挡住。
	if len(plan.AllowedHelpers) > 0 && !flowKindAllowed(plan.AllowedHelpers, in.Kind) {
		return Session{}, fmt.Errorf("%w: %s/%s 不允许 flow_kind=%s; mode-plan 仅允许 %v", ErrFeatureDisabled, in.Vendor, in.AuthMode, in.Kind, plan.AllowedHelpers)
	}
	if in.ClientIdentitySource == "" {
		in.ClientIdentitySource = plan.ClientIdentitySource
	}
	if strings.TrimSpace(in.ActorRole) == "" {
		in.ActorRole = "platform_admin"
	}
	expiresAt := in.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = s.now().UTC().Add(DefaultFlowTTL)
	}
	flowID := strings.TrimSpace(in.ID)
	if flowID == "" {
		flowID = uuid.NewString()
	}
	row := Session{
		ID: flowID, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: in.Vendor, AuthMode: in.AuthMode, Kind: in.Kind, Status: StatusStarted,
		ActorID: strings.TrimSpace(in.ActorID), ActorRole: strings.TrimSpace(in.ActorRole),
		StateHash: in.StateHash, NonceHash: in.NonceHash, EncryptedPKCEVerifier: in.EncryptedPKCEVerifier,
		ClientIdentitySource: strings.TrimSpace(in.ClientIdentitySource),
		RedirectURI:          strings.TrimSpace(in.RedirectURI), RequestedScopes: in.RequestedScopes,
		RedactedContext: in.RedactedContext, LongLivedRequested: in.LongLivedRequested,
		ExpiresAt: expiresAt,
	}
	row.IdempotencyKeyHash = HashIdempotencyKey(firstNonEmpty(in.IdempotencyKey, row.ID))
	return s.Create(ctx, row)
}

func (s *PostgresSessionStore) Create(ctx context.Context, row Session) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	if strings.TrimSpace(row.ID) == "" {
		row.ID = uuid.NewString()
	}
	if row.Status == "" {
		row.Status = StatusStarted
	}
	if row.AuthType == "" {
		row.AuthType = AuthTypePKCE
	}
	if row.ExpiresAt.IsZero() {
		row.ExpiresAt = s.now().UTC().Add(DefaultFlowTTL)
	}
	if len(row.IdempotencyKeyHash) == 0 {
		row.IdempotencyKeyHash = HashIdempotencyKey(row.ID)
	}
	if row.RedactedContext == nil {
		row.RedactedContext = map[string]any{}
	}
	cleanContext, err := ValidateRedactedContext(row.RedactedContext)
	if err != nil {
		return Session{}, err
	}
	row.RedactedContext = cleanContext
	scopes, err := json.Marshal(row.RequestedScopes)
	if err != nil {
		return Session{}, err
	}
	redacted, err := json.Marshal(row.RedactedContext)
	if err != nil {
		return Session{}, err
	}
	const q = `
INSERT INTO credential_acquisition_flow_sessions (
    id, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
    actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
    client_identity_source, redirect_uri, requested_scopes, redacted_context,
    long_lived_requested, idempotency_key_hash, expires_at
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12,
    $13, NULLIF($14, ''), $15::jsonb, $16::jsonb,
    $17, $18, $19
)
RETURNING id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
          actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
          client_identity_source, auth_type, device_code_payload,
          redirect_uri, requested_scopes, redacted_context,
          long_lived_requested, idempotency_key_hash, result_account_credential_id,
          error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
          created_at, updated_at`
	return scanSession(s.db.QueryRow(ctx, q,
		row.ID, row.TenantID, row.ProviderAccountID, row.Vendor, row.AuthMode, row.Kind, row.Status,
		row.ActorID, row.ActorRole, row.StateHash, row.NonceHash, row.EncryptedPKCEVerifier,
		row.ClientIdentitySource, row.RedirectURI, scopes, redacted,
		row.LongLivedRequested, row.IdempotencyKeyHash, row.ExpiresAt.UTC(),
	))
}

func (s *PostgresSessionStore) SetAuthPayload(ctx context.Context, id string, authType AuthType, payload map[string]any) error {
	if s == nil || s.db == nil {
		return errors.New("credentialacq: session store not configured")
	}
	if authType == "" {
		authType = AuthTypePKCE
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET auth_type = $2::oauth_acquisition_auth_type,
    device_code_payload = $3::jsonb,
    updated_at = NOW()
WHERE id = $1::uuid`
	_, err = s.db.Exec(ctx, q, strings.TrimSpace(id), string(authType), raw)
	return err
}

func (s *PostgresSessionStore) Get(ctx context.Context, id string) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	const q = `
SELECT id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
       actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
       client_identity_source, auth_type, device_code_payload,
       redirect_uri, requested_scopes, redacted_context,
       long_lived_requested, idempotency_key_hash, result_account_credential_id,
       error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
       created_at, updated_at
FROM credential_acquisition_flow_sessions
WHERE id = $1::uuid`
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrFlowNotFound
	}
	return row, err
}

func (s *PostgresSessionStore) UpdateStatus(ctx context.Context, id string, status FlowStatus, errorClass, redactedMessage string) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	if NormalizeFlowStatus(status) == "" {
		return Session{}, ErrInvalidImportBody
	}
	// terminal flow(finalized/cancelled/expired/failed)不可再被状态推进。无此 CAS predicate 时,
	// Get 与状态写之间的并发 Cancel/expire 会被 TOCTOU 绕过 —— CompleteOAuthCallback 的 UpdateStatus(
	// callback_received/validated)仍会落到一个已 cancelled 的 flow 上把它复活。terminal 行被排除后
	// RETURNING 无行 → 下面 re-fetch 区分"终态(replay)"与"真不存在(not found)"。validated 不在终态集,
	// 仍可被 oauth.go:176 由 callback_received 推进 / 失败回退为 failed。
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = $2,
    error_class = NULLIF($3, ''),
    error_message_redacted = NULLIF($4, ''),
    updated_at = NOW()
WHERE id = $1::uuid
  AND status NOT IN ('finalized', 'cancelled', 'expired', 'failed')
RETURNING id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
          actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
          client_identity_source, auth_type, device_code_payload,
          redirect_uri, requested_scopes, redacted_context,
          long_lived_requested, idempotency_key_hash, result_account_credential_id,
          error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
          created_at, updated_at`
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id), status, strings.TrimSpace(errorClass), strings.TrimSpace(redactedMessage)))
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return Session{}, getErr
	}
	if isTerminalStatus(existing.Status) {
		return existing, ErrFlowReplay
	}
	return Session{}, ErrFlowNotFound
}

func (s *PostgresSessionStore) Cancel(ctx context.Context, id string) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = 'cancelled',
    cancelled_at = NOW(),
    updated_at = NOW()
WHERE id = $1::uuid
  AND status NOT IN ('finalized', 'cancelled', 'expired', 'failed')
  AND consumed_at IS NULL
RETURNING id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
          actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
          client_identity_source, auth_type, device_code_payload,
          redirect_uri, requested_scopes, redacted_context,
          long_lived_requested, idempotency_key_hash, result_account_credential_id,
          error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
          created_at, updated_at`
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := s.Get(ctx, id)
		if getErr != nil {
			return Session{}, getErr
		}
		if isTerminalStatus(existing.Status) {
			return existing, ErrFlowReplay
		}
		if !existing.ConsumedAt.IsZero() {
			return existing, ErrFlowReplay
		}
		return existing, ErrFlowNotFound
	}
	return row, err
}

func (s *PostgresSessionStore) BeginFinalize(ctx context.Context, id string) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET consumed_at = NOW(),
    updated_at = NOW()
WHERE id = $1::uuid
  AND consumed_at IS NULL
  AND status IN ('started', 'waiting_for_user', 'callback_received', 'validated')
  AND (flow_kind <> 'oauth' OR auth_type IN ('device_code', 'sso') OR status = 'validated')
  AND expires_at > NOW()
RETURNING id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
          actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
          client_identity_source, auth_type, device_code_payload,
          redirect_uri, requested_scopes, redacted_context,
          long_lived_requested, idempotency_key_hash, result_account_credential_id,
          error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
          created_at, updated_at`
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id)))
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return Session{}, getErr
	}
	if existing.Status == StatusFinalized {
		return existing, ErrFlowReplay
	}
	if s.now().UTC().After(existing.ExpiresAt) {
		_, _ = s.UpdateStatus(ctx, id, StatusExpired, "expired", "acquisition flow expired")
		return existing, ErrFlowExpired
	}
	// callback 式 OAuth flow(PKCE)必须先经 callback 校验(status=validated)才能 finalize。
	// 仅对回调前的活跃状态报 ErrOAuthRequiresCallback;终态保持 replay 语义。
	switch existing.Status {
	case StatusStarted, StatusWaitingForUser, StatusCallbackReceived:
		if RequiresCallbackValidation(existing.Kind, existing.AuthType) {
			return existing, ErrOAuthRequiresCallback
		}
	}
	return existing, ErrFlowReplay
}

func (s *PostgresSessionStore) MarkFinalized(ctx context.Context, id string, credentialID int64) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = 'finalized',
    result_account_credential_id = $2,
    consumed_at = COALESCE(consumed_at, NOW()),
    error_class = NULL,
    error_message_redacted = NULL,
    updated_at = NOW()
WHERE id = $1::uuid
  AND cancelled_at IS NULL
  AND status NOT IN ('cancelled', 'expired', 'failed')
RETURNING id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
          actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
          client_identity_source, auth_type, device_code_payload,
          redirect_uri, requested_scopes, redacted_context,
          long_lived_requested, idempotency_key_hash, result_account_credential_id,
          error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
          created_at, updated_at`
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id), credentialID))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := s.Get(ctx, id)
		if getErr != nil {
			return Session{}, getErr
		}
		if isTerminalStatus(existing.Status) || !existing.CancelledAt.IsZero() {
			return existing, ErrFlowReplay
		}
		return Session{}, ErrFlowNotFound
	}
	return row, err
}

func (s *PostgresSessionStore) MarkFailed(ctx context.Context, id, errorClass, redactedMessage string) (Session, error) {
	return s.UpdateStatus(ctx, id, StatusFailed, errorClass, redactedMessage)
}

func scanSession(row pgx.Row) (Session, error) {
	var out Session
	var authType, redirectURI, errorClass, errorMessage pgtype.Text
	var resultCredential pgtype.Int8
	var consumedAt, cancelledAt pgtype.Timestamptz
	var requestedScopes, redactedContext, deviceCodePayload []byte
	if err := row.Scan(
		&out.ID, &out.TenantID, &out.ProviderAccountID, &out.Vendor, &out.AuthMode, &out.Kind, &out.Status,
		&out.ActorID, &out.ActorRole, &out.StateHash, &out.NonceHash, &out.EncryptedPKCEVerifier,
		&out.ClientIdentitySource, &authType, &deviceCodePayload, &redirectURI, &requestedScopes, &redactedContext,
		&out.LongLivedRequested, &out.IdempotencyKeyHash, &resultCredential,
		&errorClass, &errorMessage, &out.ExpiresAt, &consumedAt, &cancelledAt,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return Session{}, err
	}
	if authType.Valid {
		out.AuthType = AuthType(authType.String)
	}
	if redirectURI.Valid {
		out.RedirectURI = redirectURI.String
	}
	if errorClass.Valid {
		out.ErrorClass = errorClass.String
	}
	if errorMessage.Valid {
		out.ErrorMessageRedacted = errorMessage.String
	}
	if resultCredential.Valid {
		out.ResultAccountCredentialID = resultCredential.Int64
	}
	if consumedAt.Valid {
		out.ConsumedAt = consumedAt.Time
	}
	if cancelledAt.Valid {
		out.CancelledAt = cancelledAt.Time
	}
	if len(requestedScopes) > 0 {
		_ = json.Unmarshal(requestedScopes, &out.RequestedScopes)
	}
	if len(redactedContext) > 0 {
		_ = json.Unmarshal(redactedContext, &out.RedactedContext)
	}
	if len(deviceCodePayload) > 0 {
		_ = json.Unmarshal(deviceCodePayload, &out.DeviceCodePayload)
	}
	if out.RedactedContext == nil {
		out.RedactedContext = map[string]any{}
	}
	return out, nil
}

func HashIdempotencyKey(key string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return sum[:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
