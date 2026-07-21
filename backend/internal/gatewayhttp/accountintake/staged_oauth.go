package accountintake

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type OAuthDraftInput struct {
	ID        string
	TenantID  int64
	ActorID   string
	ActorRole string
	Vendor    string
	AuthMode  string
	Account   AccountDefaults
	Auxiliary json.RawMessage
	ExpiresAt time.Time
	RequestID string
	Reason    string
}

// StageOAuthPending 在上游授权前加密保存目标账号配置。此时尚无令牌，
// 也不允许领取或执行；只有通过 OAuth 回调后才能推进为可预检状态。
func (s *StagedStore) StageOAuthPending(ctx context.Context, in OAuthDraftInput) (StagedCredential, error) {
	if s == nil || s.pool == nil || s.cipher == nil || uuid.Validate(strings.TrimSpace(in.ID)) != nil ||
		in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || !validIntakeActorRole(in.ActorRole) ||
		len(in.Auxiliary) > maxStagedAuxiliaryBytes {
		return StagedCredential{}, ErrInvalidInput
	}
	vendor := credentialstore.Normalize(in.Vendor)
	mode := credentialstore.Normalize(in.AuthMode)
	if !validStagedSource("oauth", vendor, mode) || !credentialacq.SourceAllowedForMode(vendor, mode, credentialacq.FlowKindOAuth) {
		return StagedCredential{}, ErrInvalidInput
	}
	now := s.nowTime()
	expiresAt := in.ExpiresAt.UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(stagedCredentialTTL)) {
		expiresAt = now.Add(stagedCredentialTTL)
	}
	planInput := normalizeInput(PlanInput{
		TenantID: in.TenantID, SourceKind: intake.SourceOAuth,
		DefaultVendor: vendor, DefaultAuthMode: mode, Account: in.Account, Now: now,
	})
	plaintext, err := json.Marshal(stagedSecretEnvelope{Version: 1, Content: "oauth_pending", Auxiliary: in.Auxiliary})
	if err != nil {
		return StagedCredential{}, ErrInvalidInput
	}
	defer privacy.Zeroize(plaintext)
	envelope, err := s.cipher.Encrypt(ctx, plaintext, stagedAAD(in.TenantID, vendor, mode))
	if err != nil {
		return StagedCredential{}, err
	}
	planRaw, err := json.Marshal(stagedInputFromPlan(planInput))
	if err != nil {
		return StagedCredential{}, err
	}
	placeholderHash := strings.Repeat("0", 64)
	payload, _ := json.Marshal(map[string]any{
		"flow_id": in.ID, "source_kind": "oauth", "auth_mode": mode,
		"expires_at": expiresAt.Format(time.RFC3339), "credentials_present": false,
	})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StagedCredential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := cleanupStagedCredentials(ctx, tx, now); err != nil {
		return StagedCredential{}, err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO account_intake_staged_credentials (
    id, tenant_id, actor_id, actor_role, source_kind, vendor, auth_mode,
    plan_input, plan_hash, encrypted_content, encryption_scheme, key_id, nonce, aad_hash,
    status, expires_at
) VALUES ($1::uuid,$2,$3,$4,'oauth',$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,'oauth_pending',$14)`,
		strings.TrimSpace(in.ID), in.TenantID, strings.TrimSpace(in.ActorID), in.ActorRole,
		vendor, mode, planRaw, placeholderHash, envelope.Ciphertext, envelope.EncryptionScheme,
		envelope.KeyID, envelope.Nonce, envelope.AADHash, expiresAt)
	if err != nil {
		return StagedCredential{}, err
	}
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &in.TenantID, ActorID: strings.TrimSpace(in.ActorID), ActorRole: in.ActorRole,
		Action: "credential_acquisition_started", TargetType: "account_credential",
		RequestID: optionalString(strings.TrimSpace(in.RequestID)), Reason: optionalString(strings.TrimSpace(in.Reason)), Payload: payload,
	})
	if err != nil {
		return StagedCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StagedCredential{}, err
	}
	return StagedCredential{ID: strings.TrimSpace(in.ID), ExpiresAt: expiresAt}, nil
}

// StoreOAuthCandidate 在授权码交换成功后立即把候选凭据替换进同一条加密暂存。
// 先持久再做预检，进程在两步之间退出时仍可重试预检。
func (s *StagedStore) StoreOAuthCandidate(ctx context.Context, tenantID int64, actorID, id, content string) (ClaimedCredential, error) {
	if s == nil || s.pool == nil || s.cipher == nil || tenantID <= 0 || strings.TrimSpace(actorID) == "" ||
		uuid.Validate(strings.TrimSpace(id)) != nil || strings.TrimSpace(content) == "" {
		return ClaimedCredential{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimedCredential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, secret, input, err := s.loadOAuthSecret(ctx, tx, tenantID, actorID, id, "oauth_pending")
	if err != nil {
		return ClaimedCredential{}, err
	}
	if !row.expiresAt.After(s.nowTime()) {
		_ = expireStagedCredential(ctx, tx, id)
		_ = tx.Commit(ctx)
		return ClaimedCredential{}, ErrStagedCredentialExpired
	}
	plaintext, err := json.Marshal(stagedSecretEnvelope{Version: 1, Content: content, Auxiliary: secret.Auxiliary})
	if err != nil {
		return ClaimedCredential{}, ErrInvalidInput
	}
	defer privacy.Zeroize(plaintext)
	envelope, err := s.cipher.Encrypt(ctx, plaintext, stagedAAD(tenantID, row.vendor, row.authMode))
	if err != nil {
		return ClaimedCredential{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='oauth_exchanged', encrypted_content=$1, encryption_scheme=$2, key_id=$3,
    nonce=$4, aad_hash=$5, updated_at=clock_timestamp()
WHERE id=$6::uuid AND tenant_id=$7 AND actor_id=$8 AND status='oauth_pending'`,
		envelope.Ciphertext, envelope.EncryptionScheme, envelope.KeyID, envelope.Nonce, envelope.AADHash,
		strings.TrimSpace(id), tenantID, strings.TrimSpace(actorID))
	if err != nil {
		return ClaimedCredential{}, err
	}
	if tag.RowsAffected() != 1 {
		return ClaimedCredential{}, ErrStagedCredentialReplay
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimedCredential{}, err
	}
	input.Content = content
	return ClaimedCredential{ID: strings.TrimSpace(id), PlanInput: input, Auxiliary: append(json.RawMessage(nil), secret.Auxiliary...)}, nil
}

// LoadOAuthCandidate 读取已换码但尚未执行的加密候选，用于换码后预检失败的恢复。
func (s *StagedStore) LoadOAuthCandidate(ctx context.Context, tenantID int64, actorID, id string) (ClaimedCredential, error) {
	if s == nil || s.pool == nil || s.cipher == nil || tenantID <= 0 || strings.TrimSpace(actorID) == "" || uuid.Validate(strings.TrimSpace(id)) != nil {
		return ClaimedCredential{}, ErrInvalidInput
	}
	row, secret, input, err := s.loadOAuthSecret(ctx, s.pool, tenantID, actorID, id, "oauth_exchanged", "staged")
	if err != nil {
		return ClaimedCredential{}, err
	}
	if !row.expiresAt.After(s.nowTime()) {
		return ClaimedCredential{}, ErrStagedCredentialExpired
	}
	input.Content = secret.Content
	return ClaimedCredential{ID: strings.TrimSpace(id), PlanInput: input, Auxiliary: append(json.RawMessage(nil), secret.Auxiliary...)}, nil
}

func (s *StagedStore) MarkOAuthPlanned(ctx context.Context, tenantID int64, actorID, id, planHash string, input PlanInput) error {
	if s == nil || s.pool == nil || tenantID <= 0 || strings.TrimSpace(actorID) == "" ||
		uuid.Validate(strings.TrimSpace(id)) != nil || !validPlanHash(planHash) {
		return ErrInvalidInput
	}
	planRaw, err := json.Marshal(stagedInputFromPlan(input))
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='staged', plan_input=$1::jsonb, plan_hash=$2, updated_at=clock_timestamp()
WHERE id=$3::uuid AND tenant_id=$4 AND actor_id=$5 AND status IN ('oauth_exchanged','staged')`,
		planRaw, planHash, strings.TrimSpace(id), tenantID, strings.TrimSpace(actorID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrOAuthCandidateNotReady
	}
	return nil
}

type stagedOAuthRow struct {
	vendor, authMode string
	expiresAt        time.Time
}

func (s *StagedStore) loadOAuthSecret(ctx context.Context, database interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, tenantID int64, actorID, id string, statuses ...string) (stagedOAuthRow, stagedSecretEnvelope, PlanInput, error) {
	var row stagedOAuthRow
	var planRaw, ciphertext, nonce []byte
	var scheme, keyID, aadHash sql.NullString
	var storedStatus string
	err := database.QueryRow(ctx, `
SELECT vendor, auth_mode, plan_input, encrypted_content, encryption_scheme, key_id, nonce, aad_hash, status, expires_at
FROM account_intake_staged_credentials
WHERE id=$1::uuid AND tenant_id=$2 AND actor_id=$3
FOR UPDATE`, strings.TrimSpace(id), tenantID, strings.TrimSpace(actorID)).Scan(
		&row.vendor, &row.authMode, &planRaw, &ciphertext, &scheme, &keyID, &nonce, &aadHash, &storedStatus, &row.expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, ErrStagedCredentialNotFound
	}
	if err != nil {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, err
	}
	allowed := false
	for _, status := range statuses {
		allowed = allowed || storedStatus == status
	}
	if !allowed {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, ErrStagedCredentialReplay
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: ciphertext, EncryptionScheme: scheme.String, KeyID: keyID.String,
		Nonce: nonce, AADHash: aadHash.String,
	}, stagedAAD(tenantID, row.vendor, row.authMode))
	if err != nil {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, err
	}
	defer privacy.Zeroize(plaintext)
	var secret stagedSecretEnvelope
	if json.Unmarshal(plaintext, &secret) != nil || secret.Version != 1 {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, ErrInvalidInput
	}
	var staged stagedPlanInput
	if json.Unmarshal(planRaw, &staged) != nil || staged.TenantID != tenantID || staged.SourceKind != intake.SourceOAuth {
		return stagedOAuthRow{}, stagedSecretEnvelope{}, PlanInput{}, ErrInvalidInput
	}
	return row, secret, staged.withContent(""), nil
}

func expireStagedCredential(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='expired', encrypted_content=NULL, encryption_scheme=NULL, key_id=NULL,
    nonce=NULL, aad_hash=NULL, finished_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE id=$1::uuid`, strings.TrimSpace(id))
	return err
}
