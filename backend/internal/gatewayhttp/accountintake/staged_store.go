package accountintake

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const stagedCredentialTTL = 15 * time.Minute

const maxStagedAuxiliaryBytes = 256 << 10

var (
	ErrStagedCredentialNotFound = errors.New("account intake staged credential not found")
	ErrStagedCredentialExpired  = errors.New("account intake staged credential expired")
	ErrStagedCredentialReplay   = errors.New("account intake staged credential replay")
	ErrOAuthCandidateNotReady   = errors.New("account intake oauth candidate not ready")
)

type stagedPlanInput struct {
	TenantID        int64             `json:"tenant_id"`
	SourceKind      intake.SourceKind `json:"source_kind"`
	DefaultVendor   string            `json:"default_vendor"`
	DefaultAuthMode string            `json:"default_auth_mode"`
	Account         AccountDefaults   `json:"account"`
	Now             time.Time         `json:"now"`
}

func stagedInputFromPlan(in PlanInput) stagedPlanInput {
	return stagedPlanInput{
		TenantID: in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		Account: in.Account, Now: in.Now.UTC(),
	}
}

func (in stagedPlanInput) withContent(content string) PlanInput {
	return PlanInput{
		TenantID: in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		Content: content, Account: in.Account, Now: in.Now,
	}
}

type StageInput struct {
	TenantID   int64
	ActorID    string
	ActorRole  string
	SourceKind string
	Vendor     string
	AuthMode   string
	PlanInput  PlanInput
	PlanHash   string
	Content    string
	Auxiliary  json.RawMessage
	RequestID  string
	Reason     string
}

type StagedCredential struct {
	ID        string
	ExpiresAt time.Time
}

type ClaimedCredential struct {
	ID        string
	PlanInput PlanInput
	Auxiliary json.RawMessage
}

type stagedSecretEnvelope struct {
	Version   int             `json:"version"`
	Content   string          `json:"content"`
	Auxiliary json.RawMessage `json:"auxiliary,omitempty"`
}

type StagedStore struct {
	pool   *pgxpool.Pool
	cipher *credentialstore.Cipher
	now    func() time.Time
}

func NewStagedStore(pool *pgxpool.Pool, keys credentialstore.KeyProvider) *StagedStore {
	return &StagedStore{pool: pool, cipher: credentialstore.NewCipher(keys)}
}

func (s *StagedStore) Stage(ctx context.Context, in StageInput) (StagedCredential, error) {
	if s == nil || s.pool == nil || s.cipher == nil {
		return StagedCredential{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || !validIntakeActorRole(in.ActorRole) ||
		strings.TrimSpace(in.Content) == "" || len(in.Auxiliary) > maxStagedAuxiliaryBytes || !validPlanHash(in.PlanHash) {
		return StagedCredential{}, ErrInvalidInput
	}
	if !validStagedSource(in.SourceKind, in.Vendor, in.AuthMode) {
		return StagedCredential{}, ErrInvalidInput
	}

	id := uuid.NewString()
	now := s.nowTime()
	expiresAt := now.Add(stagedCredentialTTL)
	plaintext, err := json.Marshal(stagedSecretEnvelope{Version: 1, Content: in.Content, Auxiliary: in.Auxiliary})
	if err != nil {
		return StagedCredential{}, ErrInvalidInput
	}
	defer privacy.Zeroize(plaintext)
	envelope, err := s.cipher.Encrypt(ctx, plaintext, stagedAAD(in.TenantID, in.Vendor, in.AuthMode))
	if err != nil {
		return StagedCredential{}, err
	}
	planRaw, err := json.Marshal(stagedInputFromPlan(in.PlanInput))
	if err != nil {
		return StagedCredential{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"flow_id": id, "source_kind": in.SourceKind, "auth_mode": credentialstore.Normalize(in.AuthMode),
		"expires_at": expiresAt.Format(time.RFC3339), "credentials_present": true,
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
    plan_input, plan_hash, encrypted_content, encryption_scheme, key_id, nonce, aad_hash, expires_at
) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15)`,
		id, in.TenantID, strings.TrimSpace(in.ActorID), in.ActorRole, in.SourceKind,
		credentialstore.Normalize(in.Vendor), credentialstore.Normalize(in.AuthMode), planRaw, in.PlanHash,
		envelope.Ciphertext, envelope.EncryptionScheme, envelope.KeyID, envelope.Nonce, envelope.AADHash, expiresAt)
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
	return StagedCredential{ID: id, ExpiresAt: expiresAt}, nil
}

func (s *StagedStore) Claim(ctx context.Context, tenantID int64, actorID, id, planHash string) (ClaimedCredential, error) {
	if s == nil || s.pool == nil || s.cipher == nil {
		return ClaimedCredential{}, ErrNotConfigured
	}
	if tenantID <= 0 || strings.TrimSpace(actorID) == "" || uuid.Validate(strings.TrimSpace(id)) != nil || !validPlanHash(planHash) {
		return ClaimedCredential{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimedCredential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var row struct {
		vendor, authMode, storedHash, status string
		planRaw, ciphertext, nonce           []byte
		scheme, keyID, aadHash               sql.NullString
		expiresAt                            time.Time
	}
	err = tx.QueryRow(ctx, `
SELECT vendor, auth_mode, plan_input, plan_hash, encrypted_content,
       encryption_scheme, key_id, nonce, aad_hash, status, expires_at
FROM account_intake_staged_credentials
WHERE id=$1::uuid AND tenant_id=$2 AND actor_id=$3
FOR UPDATE`, strings.TrimSpace(id), tenantID, strings.TrimSpace(actorID)).Scan(
		&row.vendor, &row.authMode, &row.planRaw, &row.storedHash, &row.ciphertext,
		&row.scheme, &row.keyID, &row.nonce, &row.aadHash, &row.status, &row.expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedCredential{}, ErrStagedCredentialNotFound
	}
	if err != nil {
		return ClaimedCredential{}, err
	}
	if row.status != "staged" {
		return ClaimedCredential{}, ErrStagedCredentialReplay
	}
	if !row.expiresAt.After(s.nowTime()) {
		_, _ = tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='expired', encrypted_content=NULL, encryption_scheme=NULL, key_id=NULL,
    nonce=NULL, aad_hash=NULL, finished_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE id=$1::uuid`, strings.TrimSpace(id))
		_ = tx.Commit(ctx)
		return ClaimedCredential{}, ErrStagedCredentialExpired
	}
	if subtle.ConstantTimeCompare([]byte(row.storedHash), []byte(planHash)) != 1 {
		return ClaimedCredential{}, ErrPlanChanged
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: row.ciphertext, EncryptionScheme: row.scheme.String, KeyID: row.keyID.String,
		Nonce: row.nonce, AADHash: row.aadHash.String,
	}, stagedAAD(tenantID, row.vendor, row.authMode))
	if err != nil {
		return ClaimedCredential{}, err
	}
	defer privacy.Zeroize(plaintext)
	var secret stagedSecretEnvelope
	if json.Unmarshal(plaintext, &secret) != nil || secret.Version != 1 || strings.TrimSpace(secret.Content) == "" || len(secret.Auxiliary) > maxStagedAuxiliaryBytes {
		return ClaimedCredential{}, ErrInvalidInput
	}
	var input stagedPlanInput
	if json.Unmarshal(row.planRaw, &input) != nil || input.TenantID != tenantID {
		return ClaimedCredential{}, ErrInvalidInput
	}
	_, err = tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='claimed', encrypted_content=NULL, encryption_scheme=NULL, key_id=NULL,
    nonce=NULL, aad_hash=NULL, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE id=$1::uuid AND status='staged'`, strings.TrimSpace(id))
	if err != nil {
		return ClaimedCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimedCredential{}, err
	}
	return ClaimedCredential{
		ID: strings.TrimSpace(id), PlanInput: input.withContent(secret.Content),
		Auxiliary: append(json.RawMessage(nil), secret.Auxiliary...),
	}, nil
}

func validStagedSource(sourceKind, vendor, authMode string) bool {
	vendor = credentialstore.Normalize(vendor)
	authMode = credentialstore.Normalize(authMode)
	switch sourceKind {
	case "claude_cookie", "claude_setup_cookie":
		return vendor == credentialstore.VendorAnthropic &&
			(authMode == credentialstore.AuthModeClaudeAIOAuth || authMode == credentialstore.AuthModeClaudeSetupToken)
	case "crs_sync":
		if vendor != credentialstore.VendorAnthropic && vendor != credentialstore.VendorOpenAI && vendor != credentialstore.VendorGemini {
			return false
		}
		_, ok := credentialstore.DefaultHandlerRegistry().Lookup(vendor, authMode)
		return ok
	case "oauth":
		return credentialacq.ModeAcquisitionReleased(vendor, authMode) &&
			credentialacq.SourceAllowedForMode(vendor, authMode, credentialacq.FlowKindOAuth)
	default:
		return false
	}
}

func (s *StagedStore) Finish(ctx context.Context, tenantID int64, actorID, actorRole, id, requestID, reason string, success bool, summary ExecutionSummary) error {
	if s == nil || s.pool == nil {
		return ErrNotConfigured
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.FinishTx(ctx, tx, tenantID, actorID, actorRole, id, requestID, reason, success, summary); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FinishTx 把暂存终态与完成日志加入调用方事务，避免账号已生效但流程仍悬空。
func (s *StagedStore) FinishTx(ctx context.Context, tx pgx.Tx, tenantID int64, actorID, actorRole, id, requestID, reason string, success bool, summary ExecutionSummary) error {
	if s == nil || tx == nil {
		return ErrNotConfigured
	}
	status := "failed"
	action := "credential_acquisition_failed"
	if success {
		status = "completed"
		action = "credential_acquisition_completed"
	}
	tag, err := tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status=$1, finished_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE id=$2::uuid AND tenant_id=$3 AND actor_id=$4 AND status='claimed'`,
		status, strings.TrimSpace(id), tenantID, strings.TrimSpace(actorID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStagedCredentialReplay
	}
	payload, _ := json.Marshal(map[string]any{
		"flow_id": id, "created": summary.Created, "updated": summary.Updated,
		"skipped": summary.Skipped, "conflict": summary.Conflict, "failed": summary.Failed,
	})
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: strings.TrimSpace(actorID), ActorRole: actorRole,
		Action: action, TargetType: "account_credential", RequestID: optionalString(strings.TrimSpace(requestID)),
		Reason: optionalString(strings.TrimSpace(reason)), Payload: payload,
	})
	if err != nil {
		return err
	}
	return nil
}

// Cleanup 独立执行短期凭据生命周期清理，不依赖新的导入请求触发。
func (s *StagedStore) Cleanup(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return ErrNotConfigured
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := cleanupStagedCredentials(ctx, tx, s.nowTime()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func cleanupStagedCredentials(ctx context.Context, tx pgx.Tx, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE account_intake_staged_credentials
SET status='expired', encrypted_content=NULL, encryption_scheme=NULL, key_id=NULL,
    nonce=NULL, aad_hash=NULL, finished_at=$1, updated_at=$1
WHERE status IN ('oauth_pending','oauth_exchanged','staged') AND expires_at <= $1`, now)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE account_intake_staged_credentials
SET status='failed', finished_at=$1, updated_at=$1
WHERE status='claimed' AND claimed_at <= $1::timestamptz - INTERVAL '1 hour'
RETURNING id::text, tenant_id, actor_id, actor_role`, now)
	if err != nil {
		return err
	}
	type abandonedFlow struct {
		id, actorID, actorRole string
		tenantID               int64
	}
	abandoned := make([]abandonedFlow, 0)
	for rows.Next() {
		var flow abandonedFlow
		if err := rows.Scan(&flow.id, &flow.tenantID, &flow.actorID, &flow.actorRole); err != nil {
			rows.Close()
			return err
		}
		abandoned = append(abandoned, flow)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, flow := range abandoned {
		payload, _ := json.Marshal(map[string]any{
			"flow_id": flow.id, "error_code": "credential_flow_finish_timeout",
			"recovery_state": "operator_required",
		})
		reason := "短期凭据流程领取后一小时内未完成，系统已标记失败"
		_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &flow.tenantID, ActorID: flow.actorID, ActorRole: flow.actorRole,
			Action: "credential_acquisition_failed", TargetType: "account_credential",
			Reason: optionalString(reason), Payload: payload,
		})
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM account_intake_staged_credentials
WHERE status IN ('completed','failed','expired') AND updated_at < $1::timestamptz - INTERVAL '1 day'`, now)
	return err
}

func stagedAAD(tenantID int64, vendor, authMode string) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: tenantID, ProviderAccountID: 0, Vendor: vendor, AuthMode: authMode, Version: 1,
	}
}

func (s *StagedStore) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
