package credentialstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

const RefreshWindow = 15 * time.Minute

var (
	ErrCredentialNotFound         = errors.New("credentialstore: account credential not found")
	ErrCredentialNotActive        = errors.New("credentialstore: account credential not active")
	ErrCredentialAmbiguous        = errors.New("credentialstore: ambiguous active credential modes")
	ErrCredentialVersionConflict  = errors.New("credentialstore: credential version conflict")
	ErrCredentialAuditWriteFailed = errors.New("credentialstore: audit write failed")
)

type credentialAuditTxPhase string

const (
	credentialAuditTxPhaseBegin    credentialAuditTxPhase = "begin"
	credentialAuditTxPhaseRead     credentialAuditTxPhase = "read"
	credentialAuditTxPhaseMutation credentialAuditTxPhase = "mutation"
	credentialAuditTxPhaseAudit    credentialAuditTxPhase = "audit"
	credentialAuditTxPhaseCommit   credentialAuditTxPhase = "commit"
)

type credentialAuditTxPhaseError struct {
	phase credentialAuditTxPhase
	err   error
}

func (e credentialAuditTxPhaseError) Error() string {
	if e.err == nil {
		return string(e.phase)
	}
	return e.err.Error()
}

func (e credentialAuditTxPhaseError) Unwrap() error {
	return e.err
}

func credentialAuditPhaseError(phase credentialAuditTxPhase, err error) error {
	if err == nil {
		return nil
	}
	return credentialAuditTxPhaseError{phase: phase, err: err}
}

type CreateCredentialInput struct {
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Payload           []byte
	ActorID           string
	// ExternalAccountID/ExternalAccountEmail 是在 acquisition 时自动提取的上游 provider
	// 账号标识(非密钥,可查询的元数据)。空值以 SQL NULL 存储,而非空字符串。
	ExternalAccountID      string
	ExternalSubjectID      string
	ExternalAccountEmail   string
	ExternalIdentitySource string
	Subscription           subscriptionprofile.Observation
}

type RotateCredentialInput struct {
	TenantID               int64
	ProviderAccountID      int64
	CredentialID           int64
	ExpectedVersion        *int32
	Payload                []byte
	ActorID                string
	ExternalAccountID      string
	ExternalSubjectID      string
	ExternalAccountEmail   string
	ExternalIdentitySource string
	Subscription           subscriptionprofile.Observation
}

type CredentialRecord struct {
	ID                      int64
	TenantID                int64
	ProviderAccountID       int64
	Vendor                  string
	AuthMode                string
	State                   string
	CredentialVersion       int32
	EncryptedPayload        []byte
	EncryptionScheme        string
	KeyID                   string
	Nonce                   []byte
	AADHash                 string
	PayloadFingerprint      *string
	RefreshTokenFingerprint *string
	AccessExpiresAt         time.Time
	RefreshExpiresAt        time.Time
	RefreshBeforeAt         time.Time
	GraceUntil              time.Time
	LastRefreshAt           time.Time
	LastRefreshOutcome      *string
	FailureClass            *string
	RefreshLeadSeconds      *int32
	FailureCount            int32
	NextAttemptAt           time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
	DeletedAt               time.Time
	PlaintextPayload        []byte
	// ExternalAccountID 是上游 provider 账号标识（账号管理元数据，非密文，迁移
	// 0141 列 account_credentials.external_account_id）。nil 表示未提取到。
	// 由 ResolveActive 选出，供 provider vault 投影进 AccountInfo 供 R7 身份改写用。
	ExternalAccountID *string
}

type CredentialMetadata struct {
	ID                 int64      `json:"id"`
	TenantID           int64      `json:"tenant_id"`
	ProviderAccountID  int64      `json:"provider_account_id"`
	Vendor             string     `json:"vendor"`
	AuthMode           string     `json:"auth_mode"`
	State              string     `json:"state"`
	Version            int32      `json:"credential_version"`
	AccessExpiresAt    *time.Time `json:"access_expires_at,omitempty"`
	RefreshBeforeAt    *time.Time `json:"refresh_before_at,omitempty"`
	LastRefreshAt      *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshOutcome *string    `json:"last_refresh_outcome,omitempty"`
	FailureClass       *string    `json:"failure_class,omitempty"`
	FailureCount       int32      `json:"failure_count"`
	ProjectRef         *string    `json:"project_ref,omitempty"`
	// ExternalAccountID/ExternalAccountEmail 向 admin API/UI 暴露自动提取的上游 provider
	// 账号标识。未捕获到时为 nil。
	ExternalAccountID    *string                          `json:"external_account_id,omitempty"`
	ExternalSubjectID    *string                          `json:"external_subject_id,omitempty"`
	ExternalAccountEmail *string                          `json:"external_account_email,omitempty"`
	Subscription         *subscriptionprofile.Observation `json:"subscription,omitempty"`
	CreatedAt            time.Time                        `json:"created_at"`
	UpdatedAt            time.Time                        `json:"updated_at"`
}

const (
	DefaultRenewStatusLimit = int32(100)
	MaxRenewStatusLimit     = int32(500)
)

type ListRenewStatusParams struct {
	TenantID        *int64
	CursorUpdatedAt time.Time
	CursorID        int64
	Limit           int32
}

type RenewStatusMetadata struct {
	CredentialID       int64      `json:"id"`
	TenantID           int64      `json:"tenant_id"`
	TenantName         string     `json:"tenant_name"`
	AccountID          int64      `json:"account_id"`
	AccountName        string     `json:"account_name"`
	Vendor             string     `json:"vendor"`
	AuthMode           string     `json:"auth_mode"`
	State              string     `json:"state"`
	CredentialVersion  int32      `json:"credential_version"`
	AccessExpiresAt    *time.Time `json:"access_expires_at"`
	RefreshBeforeAt    *time.Time `json:"refresh_before_at"`
	LastRefreshAt      *time.Time `json:"last_refresh_at"`
	LastRefreshOutcome *string    `json:"last_refresh_outcome"`
	FailureClass       *string    `json:"failure_class"`
	FailureCount       int32      `json:"failure_count"`
	UpdatedAt          time.Time  `json:"-"`
}

type Store struct {
	db       db.DBTX
	cipher   *Cipher
	keys     KeyProvider
	registry *HandlerRegistry
	now      func() time.Time
}

func NewStore(database db.DBTX, keys KeyProvider, registry *HandlerRegistry) *Store {
	if registry == nil {
		registry = DefaultHandlerRegistry()
	}
	return &Store{
		db:       database,
		cipher:   NewCipher(keys),
		keys:     keys,
		registry: registry,
		now:      time.Now,
	}
}

func (s *Store) WithDB(database db.DBTX) *Store {
	cp := *s
	cp.db = database
	return &cp
}

func (s *Store) WithTransaction(ctx context.Context, fn func(*Store, db.DBTX) error) error {
	if s == nil || s.db == nil {
		return errors.New("credentialstore: db is nil")
	}
	beginner, ok := s.db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	})
	if !ok {
		return errors.New("credentialstore: db does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			// 回滚不能依赖请求 ctx, 避免 ctx 取消时事务留在连接上。
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()
	if fn != nil {
		if err := fn(s.WithDB(tx), tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) withCredentialMutationAuditTx(ctx context.Context, fn func(*Store) error) error {
	if s == nil || s.db == nil {
		return errors.New("credentialstore: db is nil")
	}
	if _, ok := s.db.(pgx.Tx); ok {
		if fn == nil {
			return nil
		}
		return fn(s)
	}
	beginner, ok := s.db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	})
	if !ok {
		if _, inTx := s.db.(pgx.Tx); !inTx {
			return credentialAuditPhaseError(credentialAuditTxPhaseBegin, errors.New("credentialstore: db does not support transactions"))
		}
		if fn == nil {
			return nil
		}
		return fn(s)
	}
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return credentialAuditPhaseError(credentialAuditTxPhaseBegin, err)
	}
	committed := false
	defer func() {
		if !committed {
			// 敏感凭据变更必须与审计同成同败；失败路径使用独立 ctx 回滚。
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()
	if fn != nil {
		if err := fn(s.WithDB(tx)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return credentialAuditPhaseError(credentialAuditTxPhaseCommit, err)
	}
	committed = true
	return nil
}

func (s *Store) HandlerRegistry() *HandlerRegistry {
	if s == nil || s.registry == nil {
		return DefaultHandlerRegistry()
	}
	return s.registry
}

func (s *Store) Create(ctx context.Context, in CreateCredentialInput) (CredentialMetadata, error) {
	if err := s.validateReady(); err != nil {
		return CredentialMetadata{}, err
	}
	in.Vendor, in.AuthMode = Normalize(in.Vendor), Normalize(in.AuthMode)
	handler, err := s.registry.MustLookup(in.Vendor, in.AuthMode)
	if err != nil {
		return CredentialMetadata{}, err
	}
	payload, err := normalizePayload(in.Payload)
	if err != nil {
		return CredentialMetadata{}, err
	}
	if err := handler.ValidatePayload(payload); err != nil {
		return CredentialMetadata{}, err
	}
	if err := s.ensureProviderAccountTenant(ctx, in.TenantID, in.ProviderAccountID); err != nil {
		return CredentialMetadata{}, err
	}
	prepared, err := s.prepareEnvelope(ctx, in.TenantID, in.ProviderAccountID, in.Vendor, in.AuthMode, 1, payload, handler)
	if err != nil {
		return CredentialMetadata{}, err
	}
	const q = `
INSERT INTO account_credentials (
    tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
    encrypted_payload, encryption_scheme, key_id, nonce, aad_hash,
    payload_fingerprint, refresh_token_fingerprint, credential_material_fingerprint,
    access_expires_at, refresh_expires_at, refresh_before_at,
    created_by_actor, last_modified_by_actor,
    external_account_id, external_subject_id, external_account_email,
    external_identity_source, project_ref
) VALUES (
    $1, $2, $3, $4, 'active', 1,
    $5, $6, $7, $8, $9,
    $10, $11, NULLIF($12, ''),
    $13, $14, $15,
    NULLIF($16, ''), NULLIF($16, ''),
    NULLIF($17, ''), NULLIF($18, ''), NULLIF($19, ''),
    NULLIF($20, ''), $21
)
RETURNING id, tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
          access_expires_at, refresh_before_at, last_refresh_at, last_refresh_outcome,
          failure_class, failure_count, external_account_id, external_subject_id,
          external_account_email, project_ref, created_at, updated_at`
	var meta CredentialMetadata
	err = s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		var rec credentialMetadataRow
		if err := txStore.db.QueryRow(ctx, q,
			in.TenantID, in.ProviderAccountID, in.Vendor, in.AuthMode,
			prepared.env.Ciphertext, prepared.env.EncryptionScheme, prepared.env.KeyID, prepared.env.Nonce, prepared.env.AADHash,
			prepared.payloadFingerprint, prepared.refreshFingerprint, prepared.materialFingerprint,
			nullableTime(prepared.accessExpiresAt), nullableTime(prepared.refreshExpiresAt), nullableTime(prepared.refreshBeforeAt),
			strings.TrimSpace(in.ActorID),
			strings.TrimSpace(in.ExternalAccountID), strings.TrimSpace(in.ExternalSubjectID), strings.TrimSpace(in.ExternalAccountEmail),
			strings.TrimSpace(in.ExternalIdentitySource),
			prepared.projectRef,
		).Scan(&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State, &rec.Version,
			&rec.AccessExpiresAt, &rec.RefreshBeforeAt, &rec.LastRefreshAt, &rec.LastRefreshOutcome,
			&rec.FailureClass, &rec.FailureCount, &rec.ExternalAccountID, &rec.ExternalSubjectID,
			&rec.ExternalAccountEmail, &rec.ProjectRef, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		meta = rec.metadata()
		meta.Subscription, err = txStore.persistSubscriptionProjection(
			ctx, in.TenantID, in.ProviderAccountID, meta.ID, meta.Version,
			in.Vendor, in.AuthMode, payload, in.Subscription,
		)
		if err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID, CredentialID: meta.ID,
			EventType: CredentialEventCreated, Vendor: in.Vendor, AuthMode: in.AuthMode,
			CredentialVersion: meta.Version, ActorID: strings.TrimSpace(in.ActorID),
			Payload: credentialSubscriptionAuditPayload(meta.Subscription),
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
	if err != nil {
		return CredentialMetadata{}, err
	}
	return meta, nil
}

func (s *Store) Rotate(ctx context.Context, in RotateCredentialInput) (CredentialMetadata, error) {
	if err := s.validateReady(); err != nil {
		return CredentialMetadata{}, err
	}
	current, err := s.getRecord(ctx, in.TenantID, in.ProviderAccountID, in.CredentialID, false)
	if err != nil {
		return CredentialMetadata{}, err
	}
	if in.ExpectedVersion != nil && current.CredentialVersion != *in.ExpectedVersion {
		return CredentialMetadata{}, ErrCredentialVersionConflict
	}
	if err := s.ensureProviderAccountTenant(ctx, current.TenantID, current.ProviderAccountID); err != nil {
		return CredentialMetadata{}, err
	}
	handler, err := s.registry.MustLookup(current.Vendor, current.AuthMode)
	if err != nil {
		return CredentialMetadata{}, err
	}
	payload, err := normalizePayload(in.Payload)
	if err != nil {
		return CredentialMetadata{}, err
	}
	if err := handler.ValidatePayload(payload); err != nil {
		return CredentialMetadata{}, err
	}
	nextVersion := current.CredentialVersion + 1
	prepared, err := s.prepareEnvelope(ctx, current.TenantID, current.ProviderAccountID, current.Vendor, current.AuthMode, nextVersion, payload, handler)
	if err != nil {
		return CredentialMetadata{}, err
	}
	const q = `
UPDATE account_credentials
SET encrypted_payload = $1,
    encryption_scheme = $2,
    key_id = $3,
    nonce = $4,
    aad_hash = $5,
    payload_fingerprint = $6,
    refresh_token_fingerprint = $7,
    credential_material_fingerprint = NULLIF($8, ''),
    access_expires_at = $9,
    refresh_expires_at = $10,
    refresh_before_at = $11,
    project_ref = $12,
    external_account_id = COALESCE(NULLIF($14, ''), external_account_id),
    external_subject_id = COALESCE(NULLIF($15, ''), external_subject_id),
    external_account_email = COALESCE(NULLIF($16, ''), external_account_email),
    external_identity_source = COALESCE(NULLIF($17, ''), external_identity_source),
    state = 'active',
    credential_version = credential_version + 1,
    failure_class = NULL,
    failure_count = 0,
    next_attempt_at = NULL,
    updated_at = NOW(),
    last_modified_by_actor = NULLIF($13, '')
WHERE id = $18
  AND tenant_id = $19
  AND provider_account_id = $20
  AND deleted_at IS NULL
  AND credential_version = $21
RETURNING id, tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
          access_expires_at, refresh_before_at, last_refresh_at, last_refresh_outcome,
          failure_class, failure_count, external_account_id, external_subject_id,
          external_account_email, project_ref, created_at, updated_at`
	var meta CredentialMetadata
	err = s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		var rec credentialMetadataRow
		if err := txStore.db.QueryRow(ctx, q,
			prepared.env.Ciphertext, prepared.env.EncryptionScheme, prepared.env.KeyID, prepared.env.Nonce, prepared.env.AADHash,
			prepared.payloadFingerprint, prepared.refreshFingerprint, prepared.materialFingerprint,
			nullableTime(prepared.accessExpiresAt), nullableTime(prepared.refreshExpiresAt), nullableTime(prepared.refreshBeforeAt),
			prepared.projectRef, strings.TrimSpace(in.ActorID),
			strings.TrimSpace(in.ExternalAccountID), strings.TrimSpace(in.ExternalSubjectID), strings.TrimSpace(in.ExternalAccountEmail),
			strings.TrimSpace(in.ExternalIdentitySource),
			current.ID, current.TenantID, current.ProviderAccountID, current.CredentialVersion,
		).Scan(&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State, &rec.Version,
			&rec.AccessExpiresAt, &rec.RefreshBeforeAt, &rec.LastRefreshAt, &rec.LastRefreshOutcome,
			&rec.FailureClass, &rec.FailureCount, &rec.ExternalAccountID, &rec.ExternalSubjectID,
			&rec.ExternalAccountEmail, &rec.ProjectRef, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return credentialAuditPhaseError(credentialAuditTxPhaseMutation, ErrCredentialVersionConflict)
			}
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		meta = rec.metadata()
		meta.Subscription, err = txStore.persistSubscriptionProjection(
			ctx, current.TenantID, current.ProviderAccountID, current.ID, meta.Version,
			current.Vendor, current.AuthMode, payload, in.Subscription,
		)
		if err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: current.TenantID, ProviderAccountID: current.ProviderAccountID, CredentialID: current.ID,
			EventType: CredentialEventRotated, Vendor: current.Vendor, AuthMode: current.AuthMode,
			CredentialVersion: meta.Version, ActorID: strings.TrimSpace(in.ActorID),
			Payload: credentialSubscriptionAuditPayload(meta.Subscription),
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
	if err != nil {
		return CredentialMetadata{}, err
	}
	return meta, nil
}

func (s *Store) ListByAccount(ctx context.Context, tenantID, providerAccountID int64) ([]CredentialMetadata, error) {
	if err := s.validateReady(); err != nil {
		return nil, err
	}
	const q = `
SELECT id, tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
       access_expires_at, refresh_before_at, last_refresh_at, last_refresh_outcome,
       failure_class, failure_count, external_account_id, external_subject_id,
       external_account_email, project_ref, created_at, updated_at
FROM account_credentials
WHERE tenant_id = $1
  AND provider_account_id = $2
  AND deleted_at IS NULL
ORDER BY updated_at DESC, id DESC`
	rows, err := s.db.Query(ctx, q, tenantID, providerAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialMetadata
	for rows.Next() {
		var rec credentialMetadataRow
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State, &rec.Version,
			&rec.AccessExpiresAt, &rec.RefreshBeforeAt, &rec.LastRefreshAt, &rec.LastRefreshOutcome,
			&rec.FailureClass, &rec.FailureCount, &rec.ExternalAccountID, &rec.ExternalSubjectID,
			&rec.ExternalAccountEmail, &rec.ProjectRef, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec.metadata())
	}
	return out, rows.Err()
}

func (s *Store) ListRenewStatus(ctx context.Context, params ListRenewStatusParams) ([]RenewStatusMetadata, error) {
	if err := s.validateReady(); err != nil {
		return nil, err
	}
	limit := normalizeRenewStatusLimit(params.Limit)
	const q = `
SELECT ac.id, ac.tenant_id, t.name, ac.provider_account_id, pa.name,
       ac.vendor, ac.auth_mode, ac.state, ac.credential_version,
       ac.access_expires_at, ac.refresh_before_at, ac.last_refresh_at,
       ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
       ac.updated_at
FROM account_credentials ac
INNER JOIN provider_accounts pa
  ON pa.id = ac.provider_account_id
 AND pa.tenant_id = ac.tenant_id
 AND pa.deleted_at IS NULL
INNER JOIN tenants t
  ON t.id = ac.tenant_id
 AND t.deleted_at IS NULL
WHERE ac.deleted_at IS NULL
  AND ($1::bigint IS NULL OR ac.tenant_id = $1::bigint)
  AND ($2::timestamptz IS NULL OR (ac.updated_at, ac.id) < ($2::timestamptz, $3::bigint))
ORDER BY ac.updated_at DESC, ac.id DESC
LIMIT $4`
	var cursorUpdatedAt any
	if params.CursorID > 0 {
		cursorUpdatedAt = params.CursorUpdatedAt
	}
	rows, err := s.db.Query(ctx, q, params.TenantID, cursorUpdatedAt, params.CursorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RenewStatusMetadata, 0, int(limit))
	for rows.Next() {
		var rec renewStatusRow
		if err := rows.Scan(
			&rec.CredentialID, &rec.TenantID, &rec.TenantName, &rec.AccountID, &rec.AccountName,
			&rec.Vendor, &rec.AuthMode, &rec.State, &rec.CredentialVersion,
			&rec.AccessExpiresAt, &rec.RefreshBeforeAt, &rec.LastRefreshAt,
			&rec.LastRefreshOutcome, &rec.FailureClass, &rec.FailureCount,
			&rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rec.metadata())
	}
	return out, rows.Err()
}

func (s *Store) SetState(ctx context.Context, tenantID, providerAccountID, credentialID int64, state, actorID string) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	state = Normalize(state)
	if !allowedState(state) {
		return fmt.Errorf("%w: state %q", ErrInvalidPayload, state)
	}
	if err := s.ensureProviderAccountTenant(ctx, tenantID, providerAccountID); err != nil {
		return err
	}
	const readQ = `
	SELECT state
	FROM account_credentials
	WHERE id = $1
	  AND tenant_id = $2
	  AND provider_account_id = $3
	  AND deleted_at IS NULL
	FOR UPDATE`
	const q = `
	UPDATE account_credentials
	SET state = $1,
	    updated_at = NOW(),
	    last_modified_by_actor = NULLIF($2, '')
	WHERE id = $3
	  AND tenant_id = $4
	  AND provider_account_id = $5
	  AND deleted_at IS NULL
	RETURNING vendor, auth_mode, credential_version`
	return s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		var oldState string
		if err := txStore.db.QueryRow(ctx, readQ, credentialID, tenantID, providerAccountID).Scan(&oldState); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return credentialAuditPhaseError(credentialAuditTxPhaseRead, ErrCredentialNotFound)
			}
			return credentialAuditPhaseError(credentialAuditTxPhaseRead, err)
		}
		var vendor, authMode string
		var version int32
		if err := txStore.db.QueryRow(ctx, q, state, strings.TrimSpace(actorID), credentialID, tenantID, providerAccountID).Scan(&vendor, &authMode, &version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return credentialAuditPhaseError(credentialAuditTxPhaseMutation, ErrCredentialNotFound)
			}
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: tenantID, ProviderAccountID: providerAccountID, CredentialID: credentialID,
			EventType: actionForStateTransition(oldState, state), Vendor: vendor, AuthMode: authMode,
			CredentialVersion: version, ActorID: strings.TrimSpace(actorID),
			Payload: map[string]any{"old_state": oldState, "new_state": state, "actor_id": strings.TrimSpace(actorID)},
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
}

func (s *Store) Delete(ctx context.Context, tenantID, providerAccountID, credentialID int64, actorID string) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	if err := s.ensureProviderAccountTenant(ctx, tenantID, providerAccountID); err != nil {
		return err
	}
	const q = `
	UPDATE account_credentials
	SET deleted_at = COALESCE(deleted_at, NOW()),
    state = 'revoked',
    updated_at = NOW(),
    last_modified_by_actor = NULLIF($1, '')
WHERE id = $2
  AND tenant_id = $3
  AND provider_account_id = $4
  AND deleted_at IS NULL
RETURNING vendor, auth_mode, credential_version`
	return s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		var vendor, authMode string
		var version int32
		if err := txStore.db.QueryRow(ctx, q, strings.TrimSpace(actorID), credentialID, tenantID, providerAccountID).Scan(&vendor, &authMode, &version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return credentialAuditPhaseError(credentialAuditTxPhaseMutation, ErrCredentialNotFound)
			}
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: tenantID, ProviderAccountID: providerAccountID, CredentialID: credentialID,
			EventType: CredentialEventDeleted, Vendor: vendor, AuthMode: authMode,
			CredentialVersion: version, ActorID: strings.TrimSpace(actorID),
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
}

const resolveActiveQuery = `
WITH scoped_credentials AS (
	SELECT ac.id, ac.tenant_id, ac.provider_account_id, ac.vendor, ac.auth_mode, ac.state,
	       ac.credential_version, ac.encrypted_payload, ac.encryption_scheme, ac.key_id,
	       ac.nonce, ac.aad_hash, ac.payload_fingerprint, ac.refresh_token_fingerprint,
	       ac.access_expires_at, ac.refresh_expires_at, ac.refresh_before_at, ac.grace_until,
	       ac.last_refresh_at, ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
	       ac.next_attempt_at, ac.created_at, ac.updated_at, ac.deleted_at,
	       ac.external_account_id,
	       pa.enabled AS provider_account_enabled
		FROM account_credentials ac
		JOIN provider_accounts pa
		  ON pa.id = ac.provider_account_id
	 AND pa.tenant_id = ac.tenant_id
	WHERE ac.provider_account_id = $1
	  AND ac.tenant_id = $2
	  AND pa.tenant_id = $2
	  AND ac.deleted_at IS NULL
	  AND pa.deleted_at IS NULL
	),
	serving_credentials AS (
		SELECT id, tenant_id, provider_account_id, vendor, auth_mode, state,
		       credential_version, encrypted_payload, encryption_scheme, key_id,
		       nonce, aad_hash, payload_fingerprint, refresh_token_fingerprint,
		       access_expires_at, refresh_expires_at, refresh_before_at, grace_until,
		       last_refresh_at, last_refresh_outcome, failure_class, failure_count,
		       next_attempt_at, created_at, updated_at, deleted_at,
		       external_account_id,
		       COUNT(*) OVER () AS active_mode_count
		FROM scoped_credentials
		WHERE provider_account_enabled
		  AND (
		      state = 'active'
		      OR (state = 'refreshing_with_grace' AND (grace_until IS NULL OR grace_until > NOW()))
		  )
	),
	selected_credential AS (
		SELECT id, tenant_id, provider_account_id, vendor, auth_mode, state,
		       credential_version, encrypted_payload, encryption_scheme, key_id,
		       nonce, aad_hash, payload_fingerprint, refresh_token_fingerprint,
		       access_expires_at, refresh_expires_at, refresh_before_at, grace_until,
		       last_refresh_at, last_refresh_outcome, failure_class, failure_count,
		       next_attempt_at, created_at, updated_at, deleted_at,
		       external_account_id,
		       active_mode_count,
		       (SELECT COUNT(*) FROM scoped_credentials) AS credential_row_count,
		       FALSE AS no_serving_credential
		FROM serving_credentials
		ORDER BY CASE state WHEN 'active' THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	),
	no_serving_credential AS (
		SELECT 0::bigint AS id, $2::bigint AS tenant_id, $1::bigint AS provider_account_id,
		       ''::text AS vendor, ''::text AS auth_mode, ''::text AS state,
		       0::integer AS credential_version, decode('', 'hex') AS encrypted_payload,
		       ''::text AS encryption_scheme, ''::text AS key_id, decode('', 'hex') AS nonce,
		       ''::text AS aad_hash, NULL::text AS payload_fingerprint,
		       NULL::text AS refresh_token_fingerprint, NULL::timestamptz AS access_expires_at,
		       NULL::timestamptz AS refresh_expires_at, NULL::timestamptz AS refresh_before_at,
		       NULL::timestamptz AS grace_until, NULL::timestamptz AS last_refresh_at,
		       NULL::text AS last_refresh_outcome, NULL::text AS failure_class,
		       0::integer AS failure_count, NULL::timestamptz AS next_attempt_at,
		       NULL::timestamptz AS created_at, NULL::timestamptz AS updated_at,
		       NULL::timestamptz AS deleted_at, NULL::text AS external_account_id,
		       0::bigint AS active_mode_count,
		       (SELECT COUNT(*) FROM scoped_credentials) AS credential_row_count,
		       TRUE AS no_serving_credential
		WHERE NOT EXISTS (SELECT 1 FROM selected_credential)
	)
	SELECT id, tenant_id, provider_account_id, vendor, auth_mode, state,
	       credential_version, encrypted_payload, encryption_scheme, key_id,
	       nonce, aad_hash, payload_fingerprint, refresh_token_fingerprint,
	       access_expires_at, refresh_expires_at, refresh_before_at, grace_until,
	       last_refresh_at, last_refresh_outcome, failure_class, failure_count,
	       next_attempt_at, created_at, updated_at, deleted_at,
	       external_account_id,
	       active_mode_count, credential_row_count, no_serving_credential
	FROM selected_credential
	UNION ALL
	SELECT id, tenant_id, provider_account_id, vendor, auth_mode, state,
	       credential_version, encrypted_payload, encryption_scheme, key_id,
	       nonce, aad_hash, payload_fingerprint, refresh_token_fingerprint,
	       access_expires_at, refresh_expires_at, refresh_before_at, grace_until,
	       last_refresh_at, last_refresh_outcome, failure_class, failure_count,
	       next_attempt_at, created_at, updated_at, deleted_at,
	       external_account_id,
	       active_mode_count, credential_row_count, no_serving_credential
		FROM no_serving_credential`

func (s *Store) ResolveActive(ctx context.Context, tenantID, providerAccountID int64) (CredentialRecord, error) {
	if err := s.validateReady(); err != nil {
		return CredentialRecord{}, err
	}
	// DR-001 防御: caller 必须显式传 tenantID; 即使 caller 误传他租户的
	// providerAccountID, 这里也用 pa.tenant_id=$2 + ac.tenant_id=$2 双侧绑死。
	if tenantID == 0 {
		return CredentialRecord{}, fmt.Errorf("%w: tenantID required", ErrInvalidPayload)
	}
	var rec CredentialRecord
	var activeModeCount, credentialRowCount int64
	var noServingCredential bool
	var accessExp, refreshExp, refreshBefore, graceUntil, lastRefresh, nextAttempt, createdAt, updatedAt, deletedAt pgtype.Timestamptz
	err := s.db.QueryRow(ctx, resolveActiveQuery, providerAccountID, tenantID).Scan(
		&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State,
		&rec.CredentialVersion, &rec.EncryptedPayload, &rec.EncryptionScheme, &rec.KeyID,
		&rec.Nonce, &rec.AADHash, &rec.PayloadFingerprint, &rec.RefreshTokenFingerprint,
		&accessExp, &refreshExp, &refreshBefore, &graceUntil,
		&lastRefresh, &rec.LastRefreshOutcome, &rec.FailureClass, &rec.FailureCount,
		&nextAttempt, &createdAt, &updatedAt, &deletedAt,
		&rec.ExternalAccountID,
		&activeModeCount, &credentialRowCount, &noServingCredential,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialRecord{}, ErrCredentialNotFound
		}
		return CredentialRecord{}, err
	}
	if noServingCredential {
		if credentialRowCount > 0 {
			return CredentialRecord{}, ErrCredentialNotActive
		}
		return CredentialRecord{}, ErrCredentialNotFound
	}
	if activeModeCount > 1 {
		return CredentialRecord{}, fmt.Errorf("%w: tenant_id=%d provider_account_id=%d active_modes=%d",
			ErrCredentialAmbiguous, tenantID, providerAccountID, activeModeCount)
	}
	rec.AccessExpiresAt = pgTime(accessExp)
	rec.RefreshExpiresAt = pgTime(refreshExp)
	rec.RefreshBeforeAt = pgTime(refreshBefore)
	rec.GraceUntil = pgTime(graceUntil)
	rec.LastRefreshAt = pgTime(lastRefresh)
	rec.NextAttemptAt = pgTime(nextAttempt)
	rec.CreatedAt = pgTime(createdAt)
	rec.UpdatedAt = pgTime(updatedAt)
	rec.DeletedAt = pgTime(deletedAt)
	plaintext, err := s.decryptRecord(ctx, rec)
	if err != nil {
		return CredentialRecord{}, err
	}
	rec.PlaintextPayload = plaintext
	_ = s.InsertAuditEvent(ctx, AuditEvent{
		TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID, CredentialID: rec.ID,
		EventType: "credential_resolved", Vendor: rec.Vendor, AuthMode: rec.AuthMode,
		CredentialVersion: rec.CredentialVersion,
		Payload:           map[string]any{"state": rec.State},
	})
	return rec, nil
}

func (s *Store) LoadForRefresh(ctx context.Context, providerAccountID int64) (CredentialRecord, error) {
	if err := s.validateReady(); err != nil {
		return CredentialRecord{}, err
	}
	const q = `
SELECT ac.id, ac.tenant_id, ac.provider_account_id, ac.vendor, ac.auth_mode, ac.state,
       ac.credential_version, ac.encrypted_payload, ac.encryption_scheme, ac.key_id,
       ac.nonce, ac.aad_hash, ac.payload_fingerprint, ac.refresh_token_fingerprint,
       ac.access_expires_at, ac.refresh_expires_at, ac.refresh_before_at, ac.grace_until,
       ac.last_refresh_at, ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
       ac.next_attempt_at, ac.created_at, ac.updated_at, ac.deleted_at,
       pa.refresh_lead_seconds
	FROM account_credentials ac
	JOIN provider_accounts pa
	  ON pa.id = ac.provider_account_id
	 AND pa.tenant_id = ac.tenant_id
	WHERE ac.provider_account_id = $1
	  AND ac.deleted_at IS NULL
	  AND pa.deleted_at IS NULL
	  AND pa.enabled
	  AND pa.health_state <> 'revoked'
	  AND (
	      pa.health_state = 'healthy'
	      OR (
	          pa.health_state IN ('throttled', 'cooldown')
	          AND pa.health_state_until IS NOT NULL
	          AND pa.health_state_until <= NOW()
	      )
	  )
	  AND ac.state IN ('active', 'refreshing_with_grace', 'temp_unschedulable')
	  AND ac.refresh_before_at IS NOT NULL
	ORDER BY ac.refresh_before_at ASC, ac.updated_at ASC
LIMIT 1`
	rec, err := s.scanRecordForRefresh(ctx, q, providerAccountID)
	if err != nil {
		return CredentialRecord{}, err
	}
	plaintext, err := s.decryptRecord(ctx, rec)
	if err != nil {
		return CredentialRecord{}, err
	}
	rec.PlaintextPayload = plaintext
	return rec, nil
}

func (s *Store) scanRecordForRefresh(ctx context.Context, query string, args ...any) (CredentialRecord, error) {
	var rec CredentialRecord
	var accessExp, refreshExp, refreshBefore, graceUntil, lastRefresh, nextAttempt, createdAt, updatedAt, deletedAt pgtype.Timestamptz
	var refreshLeadSeconds *int32
	err := s.db.QueryRow(ctx, query, args...).Scan(
		&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State,
		&rec.CredentialVersion, &rec.EncryptedPayload, &rec.EncryptionScheme, &rec.KeyID,
		&rec.Nonce, &rec.AADHash, &rec.PayloadFingerprint, &rec.RefreshTokenFingerprint,
		&accessExp, &refreshExp, &refreshBefore, &graceUntil,
		&lastRefresh, &rec.LastRefreshOutcome, &rec.FailureClass, &rec.FailureCount,
		&nextAttempt, &createdAt, &updatedAt, &deletedAt,
		&refreshLeadSeconds,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialRecord{}, ErrCredentialNotFound
		}
		return CredentialRecord{}, err
	}
	rec.AccessExpiresAt = pgTime(accessExp)
	rec.RefreshExpiresAt = pgTime(refreshExp)
	rec.RefreshBeforeAt = pgTime(refreshBefore)
	rec.GraceUntil = pgTime(graceUntil)
	rec.LastRefreshAt = pgTime(lastRefresh)
	rec.NextAttemptAt = pgTime(nextAttempt)
	rec.CreatedAt = pgTime(createdAt)
	rec.UpdatedAt = pgTime(updatedAt)
	rec.DeletedAt = pgTime(deletedAt)
	rec.RefreshLeadSeconds = refreshLeadSeconds
	return rec, nil
}

func (s *Store) LoadForProviderAccountTest(ctx context.Context, tenantID, providerAccountID int64) (CredentialRecord, error) {
	if err := s.validateReady(); err != nil {
		return CredentialRecord{}, err
	}
	const q = `
	SELECT ac.id, ac.tenant_id, ac.provider_account_id, ac.vendor, ac.auth_mode, ac.state,
	       ac.credential_version, ac.encrypted_payload, ac.encryption_scheme, ac.key_id,
	       ac.nonce, ac.aad_hash, ac.payload_fingerprint, ac.refresh_token_fingerprint,
	       ac.access_expires_at, ac.refresh_expires_at, ac.refresh_before_at, ac.grace_until,
	       ac.last_refresh_at, ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
	       ac.next_attempt_at, ac.created_at, ac.updated_at, ac.deleted_at,
	       COUNT(*) OVER () AS test_mode_count
		FROM account_credentials ac
		JOIN provider_accounts pa
		  ON pa.id = ac.provider_account_id
		 AND pa.tenant_id = ac.tenant_id
		WHERE ac.provider_account_id = $1
		  AND ac.tenant_id = $2
		  AND ac.deleted_at IS NULL
		  AND pa.deleted_at IS NULL
		  AND ac.state IN ('active', 'refreshing_with_grace', 'temp_unschedulable', 'operator_attention')
		ORDER BY
		  CASE ac.state
		    WHEN 'active' THEN 0
	    WHEN 'refreshing_with_grace' THEN 1
	    WHEN 'temp_unschedulable' THEN 2
	    WHEN 'operator_attention' THEN 3
	    ELSE 4
	  END,
		  ac.updated_at DESC,
		  ac.id DESC
	LIMIT 1`
	rec, testModeCount, err := s.scanRecordWithCount(ctx, q, providerAccountID, tenantID)
	if err != nil {
		return CredentialRecord{}, err
	}
	if testModeCount > 1 {
		return CredentialRecord{}, fmt.Errorf("%w: tenant_id=%d provider_account_id=%d test_modes=%d",
			ErrCredentialAmbiguous, tenantID, providerAccountID, testModeCount)
	}
	plaintext, err := s.decryptRecord(ctx, rec)
	if err != nil {
		return CredentialRecord{}, err
	}
	rec.PlaintextPayload = plaintext
	return rec, nil
}

// effectiveRefreshLead 返回计算 refresh_before_at 时所用的提前量时长。当 per-account
// 覆盖值(perAccount)非 nil 且为正时,它优先生效;否则原样返回全局 window,从而为
// NULL 账号保持完全一致的既有行为。
func effectiveRefreshLead(perAccount *int32, global time.Duration) time.Duration {
	if perAccount != nil && *perAccount > 0 {
		return time.Duration(*perAccount) * time.Second
	}
	return global
}

func (s *Store) SaveRefreshSuccess(ctx context.Context, rec CredentialRecord, payload []byte, accessExpiresAt time.Time, outcome string) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	handler, err := s.registry.MustLookup(rec.Vendor, rec.AuthMode)
	if err != nil {
		return err
	}
	payload, err = normalizePayload(payload)
	if err != nil {
		return err
	}
	if err := handler.ValidatePayload(payload); err != nil {
		return err
	}
	if err := s.ensureProviderAccountTenant(ctx, rec.TenantID, rec.ProviderAccountID); err != nil {
		return err
	}
	version := rec.CredentialVersion + 1
	prepared, err := s.prepareEnvelope(ctx, rec.TenantID, rec.ProviderAccountID, rec.Vendor, rec.AuthMode, version, payload, handler)
	if err != nil {
		return err
	}
	if !accessExpiresAt.IsZero() {
		prepared.accessExpiresAt = accessExpiresAt.UTC()
		lead := effectiveRefreshLead(rec.RefreshLeadSeconds, RefreshWindow)
		prepared.refreshBeforeAt = prepared.accessExpiresAt.Add(-lead)
	}
	const q = `
UPDATE account_credentials
SET encrypted_payload = $1,
    encryption_scheme = $2,
    key_id = $3,
    nonce = $4,
    aad_hash = $5,
    payload_fingerprint = $6,
    refresh_token_fingerprint = $7,
    credential_material_fingerprint = NULLIF($8, ''),
    access_expires_at = $9,
    refresh_expires_at = $10,
    refresh_before_at = $11,
    project_ref = $12,
    state = 'active',
    credential_version = credential_version + 1,
    last_refresh_at = NOW(),
    last_refresh_outcome = $13,
    failure_class = NULL,
    failure_count = 0,
    next_attempt_at = $18,
    updated_at = NOW()
WHERE id = $14
  AND tenant_id = $15
  AND provider_account_id = $16
  AND deleted_at IS NULL
  AND credential_version = $17`
	now := time.Now().UTC()
	// 计算 next_attempt_at:正常有效 refresh 时为 NULL,无效时做节流。
	// 当 accessExpiresAt 为零(无过期信息)时 refreshBeforeAt 为零;此时视为有效。
	var nextAttemptAt time.Time // 零值 -> 经 nullableTime 转为 NULL
	if !prepared.refreshBeforeAt.IsZero() {
		nextAttemptAt = ineffectiveRefreshNextAttempt(prepared.refreshBeforeAt, now, time.Time{})
	}
	return s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		tag, err := txStore.db.Exec(ctx, q,
			prepared.env.Ciphertext, prepared.env.EncryptionScheme, prepared.env.KeyID, prepared.env.Nonce, prepared.env.AADHash,
			prepared.payloadFingerprint, prepared.refreshFingerprint, prepared.materialFingerprint,
			nullableTime(prepared.accessExpiresAt), nullableTime(prepared.refreshExpiresAt), nullableTime(prepared.refreshBeforeAt),
			prepared.projectRef, outcome, rec.ID, rec.TenantID, rec.ProviderAccountID, rec.CredentialVersion, nullableTime(nextAttemptAt),
		)
		if err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if tag.RowsAffected() != 1 {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, errors.New("credentialstore: refresh credential cas lost"))
		}
		if observation, ok := freshSubscriptionRefreshObservation(rec.Vendor, rec.AuthMode, rec.PlaintextPayload, payload); ok {
			if _, err := txStore.recordSubscriptionObservation(ctx, rec.TenantID, rec.ProviderAccountID, rec.ID, version, observation); err != nil {
				return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
			}
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID, CredentialID: rec.ID,
			EventType: CredentialEventRefreshSucceeded, Vendor: rec.Vendor, AuthMode: rec.AuthMode,
			CredentialVersion: version, Payload: map[string]any{"outcome": outcome},
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
}

func (s *Store) SaveRefreshFailure(ctx context.Context, rec CredentialRecord, failureClass string, nextAttemptAt time.Time) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	if err := s.ensureProviderAccountTenant(ctx, rec.TenantID, rec.ProviderAccountID); err != nil {
		return err
	}
	state := refreshFailureState(failureClass)
	const q = `
UPDATE account_credentials
SET state = $1,
    failure_class = $2,
    failure_count = failure_count + 1,
    next_attempt_at = $3,
    last_refresh_at = NOW(),
    last_refresh_outcome = 'refresh_failed',
    updated_at = NOW()
WHERE id = $4
  AND tenant_id = $5
  AND provider_account_id = $6
  AND deleted_at IS NULL
  AND credential_version = $7`
	return s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		tag, err := txStore.db.Exec(ctx, q, state, failureClass, nullableTime(nextAttemptAt), rec.ID, rec.TenantID, rec.ProviderAccountID, rec.CredentialVersion)
		if err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if tag.RowsAffected() != 1 {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, ErrCredentialNotFound)
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID, CredentialID: rec.ID,
			EventType: CredentialEventRefreshFailed, Vendor: rec.Vendor, AuthMode: rec.AuthMode,
			CredentialVersion: rec.CredentialVersion, Payload: map[string]any{"failure_class": failureClass, "state": state},
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
}

func refreshFailureState(failureClass string) string {
	switch failureClass {
	case "invalid_grant", "auth_expired":
		return StateRevoked
	case "decrypt_failed", "payload_invalid", "operator_config_required":
		return StateOperatorAttention
	default:
		return StateTempUnschedulable
	}
}

func (s *Store) decryptRecord(ctx context.Context, rec CredentialRecord) ([]byte, error) {
	env := Envelope{
		Ciphertext:       rec.EncryptedPayload,
		Nonce:            rec.Nonce,
		KeyID:            rec.KeyID,
		EncryptionScheme: rec.EncryptionScheme,
		AADHash:          rec.AADHash,
	}
	return s.cipher.Decrypt(ctx, env, AAD{
		TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID,
		Vendor: rec.Vendor, AuthMode: rec.AuthMode, Version: rec.CredentialVersion,
	})
}

func (s *Store) scanRecord(ctx context.Context, query string, args ...any) (CredentialRecord, error) {
	var rec CredentialRecord
	var accessExp, refreshExp, refreshBefore, graceUntil, lastRefresh, nextAttempt, createdAt, updatedAt, deletedAt pgtype.Timestamptz
	err := s.db.QueryRow(ctx, query, args...).Scan(
		&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State,
		&rec.CredentialVersion, &rec.EncryptedPayload, &rec.EncryptionScheme, &rec.KeyID,
		&rec.Nonce, &rec.AADHash, &rec.PayloadFingerprint, &rec.RefreshTokenFingerprint,
		&accessExp, &refreshExp, &refreshBefore, &graceUntil,
		&lastRefresh, &rec.LastRefreshOutcome, &rec.FailureClass, &rec.FailureCount,
		&nextAttempt, &createdAt, &updatedAt, &deletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialRecord{}, ErrCredentialNotFound
		}
		return CredentialRecord{}, err
	}
	rec.AccessExpiresAt = pgTime(accessExp)
	rec.RefreshExpiresAt = pgTime(refreshExp)
	rec.RefreshBeforeAt = pgTime(refreshBefore)
	rec.GraceUntil = pgTime(graceUntil)
	rec.LastRefreshAt = pgTime(lastRefresh)
	rec.NextAttemptAt = pgTime(nextAttempt)
	rec.CreatedAt = pgTime(createdAt)
	rec.UpdatedAt = pgTime(updatedAt)
	rec.DeletedAt = pgTime(deletedAt)
	return rec, nil
}

func (s *Store) scanRecordWithCount(ctx context.Context, query string, args ...any) (CredentialRecord, int64, error) {
	var rec CredentialRecord
	var rowCount int64
	var accessExp, refreshExp, refreshBefore, graceUntil, lastRefresh, nextAttempt, createdAt, updatedAt, deletedAt pgtype.Timestamptz
	err := s.db.QueryRow(ctx, query, args...).Scan(
		&rec.ID, &rec.TenantID, &rec.ProviderAccountID, &rec.Vendor, &rec.AuthMode, &rec.State,
		&rec.CredentialVersion, &rec.EncryptedPayload, &rec.EncryptionScheme, &rec.KeyID,
		&rec.Nonce, &rec.AADHash, &rec.PayloadFingerprint, &rec.RefreshTokenFingerprint,
		&accessExp, &refreshExp, &refreshBefore, &graceUntil,
		&lastRefresh, &rec.LastRefreshOutcome, &rec.FailureClass, &rec.FailureCount,
		&nextAttempt, &createdAt, &updatedAt, &deletedAt, &rowCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialRecord{}, 0, ErrCredentialNotFound
		}
		return CredentialRecord{}, 0, err
	}
	rec.AccessExpiresAt = pgTime(accessExp)
	rec.RefreshExpiresAt = pgTime(refreshExp)
	rec.RefreshBeforeAt = pgTime(refreshBefore)
	rec.GraceUntil = pgTime(graceUntil)
	rec.LastRefreshAt = pgTime(lastRefresh)
	rec.NextAttemptAt = pgTime(nextAttempt)
	rec.CreatedAt = pgTime(createdAt)
	rec.UpdatedAt = pgTime(updatedAt)
	rec.DeletedAt = pgTime(deletedAt)
	return rec, rowCount, nil
}

func (s *Store) getRecord(ctx context.Context, tenantID, providerAccountID, credentialID int64, decrypt bool) (CredentialRecord, error) {
	const q = `
	SELECT ac.id, ac.tenant_id, ac.provider_account_id, ac.vendor, ac.auth_mode, ac.state,
	       ac.credential_version, ac.encrypted_payload, ac.encryption_scheme, ac.key_id,
	       ac.nonce, ac.aad_hash, ac.payload_fingerprint, ac.refresh_token_fingerprint,
	       ac.access_expires_at, ac.refresh_expires_at, ac.refresh_before_at, ac.grace_until,
	       ac.last_refresh_at, ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
	       ac.next_attempt_at, ac.created_at, ac.updated_at, ac.deleted_at
	FROM account_credentials ac
	JOIN provider_accounts pa
	  ON pa.id = ac.provider_account_id
	 AND pa.tenant_id = ac.tenant_id
	WHERE ac.id = $1
	  AND ac.tenant_id = $2
	  AND ac.provider_account_id = $3
	  AND ac.deleted_at IS NULL
	  AND pa.deleted_at IS NULL`
	rec, err := s.scanRecord(ctx, q, credentialID, tenantID, providerAccountID)
	if err != nil {
		return CredentialRecord{}, err
	}
	if decrypt {
		payload, err := s.decryptRecord(ctx, rec)
		if err != nil {
			return CredentialRecord{}, err
		}
		rec.PlaintextPayload = payload
	}
	return rec, nil
}

func (s *Store) ensureProviderAccountTenant(ctx context.Context, tenantID, providerAccountID int64) error {
	const q = `
	SELECT id
	FROM provider_accounts
	WHERE id = $1
	  AND tenant_id = $2
	  AND deleted_at IS NULL`
	var id int64
	if err := s.db.QueryRow(ctx, q, providerAccountID, tenantID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCredentialNotFound
		}
		return err
	}
	return nil
}

type preparedEnvelope struct {
	env                 Envelope
	payloadFingerprint  *string
	refreshFingerprint  *string
	materialFingerprint *string
	accessExpiresAt     time.Time
	refreshExpiresAt    time.Time
	refreshBeforeAt     time.Time
	projectRef          *string
}

func (s *Store) prepareEnvelope(ctx context.Context, tenantID, providerAccountID int64, vendor, authMode string, version int32, payload []byte, handler ModeHandler) (preparedEnvelope, error) {
	env, err := s.cipher.Encrypt(ctx, payload, AAD{
		TenantID: tenantID, ProviderAccountID: providerAccountID,
		Vendor: vendor, AuthMode: authMode, Version: version,
	})
	if err != nil {
		return preparedEnvelope{}, err
	}
	currentKey, err := s.keys.CurrentKey(ctx)
	if err != nil {
		return preparedEnvelope{}, err
	}
	defer privacy.Zeroize(currentKey.Material)
	fields, _ := parsePayloadFields(payload)
	payloadFP := HMACFingerprint(currentKey, "payload", payload)
	refreshFP := HMACFingerprint(currentKey, "refresh_token", []byte(fieldString(fields, "refresh_token")))
	accessExp := expiresAt(fields)
	refreshExp := parseNamedTime(fields, "refresh_expires_at")
	var refreshBefore time.Time
	if handler.Refreshable() {
		if accessExp.IsZero() {
			// 无初始 access token 的可刷新凭据(如 vertex SA 仅有 client_email+private_key
			// 私钥材料):排入即时刷新,让 refresher 铸出首个 token;否则永不进刷新扫描=
			// 无法物化 fail-closed(M1 bootstrap)。铸不出的凭据经 refresher 的 backoff 限频。
			refreshBefore = time.Now().UTC()
		} else {
			refreshBefore = accessExp.Add(-RefreshWindow)
		}
	}
	return preparedEnvelope{
		env:                 env,
		payloadFingerprint:  stringPtr(payloadFP),
		refreshFingerprint:  stringPtr(refreshFP),
		materialFingerprint: stringPtr(CredentialMaterialFingerprint(tenantID, vendor, authMode, payload)),
		accessExpiresAt:     accessExp,
		refreshExpiresAt:    refreshExp,
		refreshBeforeAt:     refreshBefore,
		projectRef:          stringPtr(fieldString(fields, "project_id")),
	}, nil
}

func (s *Store) validateReady() error {
	switch {
	case s == nil:
		return errors.New("credentialstore: store is nil")
	case s.db == nil:
		return errors.New("credentialstore: db is nil")
	case s.cipher == nil || s.keys == nil:
		return fmt.Errorf("%w: store cipher not configured", ErrKeyUnavailable)
	case s.registry == nil:
		return errors.New("credentialstore: handler registry missing")
	default:
		return nil
	}
}

func normalizePayload(raw []byte) ([]byte, error) {
	fields, err := parsePayloadFields(raw)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return out, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func pgTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func parseNamedTime(fields map[string]json.RawMessage, key string) time.Time {
	raw := fieldString(fields, key)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func stringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	out := strings.TrimSpace(v)
	return &out
}

func allowedState(state string) bool {
	switch state {
	case StateActive, StateRefreshing, StateRefreshingWithGrace, StateExpired,
		StateTempUnschedulable, StateNeedsRotation, StateRevoked, StateOperatorAttention:
		return true
	default:
		return false
	}
}

type credentialMetadataRow struct {
	ID                   int64
	TenantID             int64
	ProviderAccountID    int64
	Vendor               string
	AuthMode             string
	State                string
	Version              int32
	AccessExpiresAt      pgtype.Timestamptz
	RefreshBeforeAt      pgtype.Timestamptz
	LastRefreshAt        pgtype.Timestamptz
	LastRefreshOutcome   *string
	FailureClass         *string
	FailureCount         int32
	ExternalAccountID    *string
	ExternalSubjectID    *string
	ExternalAccountEmail *string
	ProjectRef           *string
	CreatedAt            pgtype.Timestamptz
	UpdatedAt            pgtype.Timestamptz
}

func (r credentialMetadataRow) metadata() CredentialMetadata {
	return CredentialMetadata{
		ID: r.ID, TenantID: r.TenantID, ProviderAccountID: r.ProviderAccountID,
		Vendor: r.Vendor, AuthMode: r.AuthMode, State: r.State, Version: r.Version,
		AccessExpiresAt: optionalTime(r.AccessExpiresAt), RefreshBeforeAt: optionalTime(r.RefreshBeforeAt),
		LastRefreshAt: optionalTime(r.LastRefreshAt), LastRefreshOutcome: r.LastRefreshOutcome,
		FailureClass: r.FailureClass, FailureCount: r.FailureCount,
		ProjectRef:           trimmedNonEmpty(r.ProjectRef),
		ExternalAccountID:    trimmedNonEmpty(r.ExternalAccountID),
		ExternalSubjectID:    trimmedNonEmpty(r.ExternalSubjectID),
		ExternalAccountEmail: trimmedNonEmpty(r.ExternalAccountEmail),
		CreatedAt:            pgTime(r.CreatedAt),
		UpdatedAt:            pgTime(r.UpdatedAt),
	}
}

// trimmedNonEmpty 仅当指针指向去除空白后非空的字符串时才返回该指针;否则返回 nil,
// 以便空列在 JSON 中表现为被省略,而非 ""。
func trimmedNonEmpty(in *string) *string {
	if in == nil {
		return nil
	}
	if strings.TrimSpace(*in) == "" {
		return nil
	}
	return in
}

func optionalTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time.UTC()
	return &out
}

type renewStatusRow struct {
	CredentialID       int64
	TenantID           int64
	TenantName         string
	AccountID          int64
	AccountName        string
	Vendor             string
	AuthMode           string
	State              string
	CredentialVersion  int32
	AccessExpiresAt    pgtype.Timestamptz
	RefreshBeforeAt    pgtype.Timestamptz
	LastRefreshAt      pgtype.Timestamptz
	LastRefreshOutcome *string
	FailureClass       *string
	FailureCount       int32
	UpdatedAt          pgtype.Timestamptz
}

func (r renewStatusRow) metadata() RenewStatusMetadata {
	return RenewStatusMetadata{
		CredentialID: r.CredentialID, TenantID: r.TenantID, TenantName: r.TenantName,
		AccountID: r.AccountID, AccountName: r.AccountName,
		Vendor: r.Vendor, AuthMode: r.AuthMode, State: r.State, CredentialVersion: r.CredentialVersion,
		AccessExpiresAt: optionalTime(r.AccessExpiresAt), RefreshBeforeAt: optionalTime(r.RefreshBeforeAt),
		LastRefreshAt: optionalTime(r.LastRefreshAt), LastRefreshOutcome: r.LastRefreshOutcome,
		FailureClass: r.FailureClass, FailureCount: r.FailureCount, UpdatedAt: pgTime(r.UpdatedAt),
	}
}

func normalizeRenewStatusLimit(limit int32) int32 {
	if limit <= 0 {
		return DefaultRenewStatusLimit
	}
	if limit > MaxRenewStatusLimit+1 {
		return MaxRenewStatusLimit + 1
	}
	return limit
}

type AuditEvent struct {
	TenantID          int64
	ProviderAccountID int64
	CredentialID      int64
	EventType         string
	Vendor            string
	AuthMode          string
	CredentialVersion int32
	ActorID           string
	RequestID         string
	Payload           map[string]any
}

func (s *Store) InsertAuditEvent(ctx context.Context, e AuditEvent) error {
	if s == nil || s.db == nil || e.TenantID <= 0 || e.ProviderAccountID <= 0 || strings.TrimSpace(e.EventType) == "" {
		return nil
	}
	payload := e.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	rawPayload, err := privacy.DefaultRedactor().SanitizePayload(ctx, payload)
	if err != nil {
		rawPayload = privacy.BlockedPayload(privacy.ErrorClassPrivacyGuardHit)
	}
	const q = `
INSERT INTO credential_audit_events (
    tenant_id, provider_account_id, account_credential_id, event_type,
    vendor, auth_mode, credential_version, actor_id, request_id, payload
) VALUES (
    $1, $2, NULLIF($3, 0), $4,
    NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, 0), NULLIF($8, ''), NULLIF($9, ''), $10::jsonb
)`
	_, err = s.db.Exec(ctx, q, e.TenantID, e.ProviderAccountID, e.CredentialID, e.EventType,
		Normalize(e.Vendor), Normalize(e.AuthMode), e.CredentialVersion, strings.TrimSpace(e.ActorID), strings.TrimSpace(e.RequestID), rawPayload)
	return err
}

func (s *Store) insertAuditEventStrict(ctx context.Context, e AuditEvent) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: store db missing", ErrCredentialAuditWriteFailed)
	}
	if e.TenantID <= 0 || e.ProviderAccountID <= 0 || strings.TrimSpace(e.EventType) == "" {
		return fmt.Errorf("%w: invalid event tenant=%d provider_account=%d event_type=%q", ErrCredentialAuditWriteFailed, e.TenantID, e.ProviderAccountID, e.EventType)
	}
	if err := s.InsertAuditEvent(ctx, e); err != nil {
		return fmt.Errorf("%w: %v", ErrCredentialAuditWriteFailed, err)
	}
	return nil
}
