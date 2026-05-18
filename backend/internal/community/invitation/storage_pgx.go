package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Generate(ctx context.Context, rec generateRecord) (Invitation, error) {
	if s == nil || s.pool == nil {
		return Invitation{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Invitation{}, fmt.Errorf("invitation: begin generate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 同租户邀请码创建串行化，避免并发请求一起越过月配额。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('invitation_quota:' || $1::text, 0))`, rec.TenantID); err != nil {
		return Invitation{}, fmt.Errorf("invitation: quota lock: %w", err)
	}
	clientKey := sql.NullString{}
	if rec.ClientIdempotencyKey != nil {
		clientKey = sql.NullString{String: *rec.ClientIdempotencyKey, Valid: true}
		row := tx.QueryRow(ctx, `
	SELECT id, tenant_id, code, inviter_user_id, created_at, expires_at, usage_count, max_usage
	FROM invitations
	WHERE tenant_id=$1 AND client_idempotency_key=$2`, rec.TenantID, clientKey.String)
		inv, err := scanInvitation(row)
		if err == nil {
			return inv, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Invitation{}, fmt.Errorf("invitation: get by idempotency key: %w", err)
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `
	SELECT COUNT(*)
FROM invitations
WHERE tenant_id=$1 AND created_at >= $2`, rec.TenantID, monthStartUTC(rec.CreatedAt)).Scan(&count); err != nil {
		return Invitation{}, fmt.Errorf("invitation: quota recheck: %w", err)
	}
	if count >= MonthlyTenantQuota {
		return Invitation{}, ErrQuotaExceeded
	}
	row := tx.QueryRow(ctx, `
	INSERT INTO invitations (
		tenant_id, code, inviter_user_id, created_at, expires_at, max_usage, client_idempotency_key
	) VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING id, tenant_id, code, inviter_user_id, created_at, expires_at, usage_count, max_usage`,
		rec.TenantID, rec.Code, rec.InviterUserID, rec.CreatedAt, rec.ExpiresAt, rec.MaxUsage, clientKey)
	inv, err := scanInvitation(row)
	if isUniqueViolation(err) {
		return Invitation{}, ErrDuplicateCode
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("invitation: insert invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invitation{}, fmt.Errorf("invitation: commit generate: %w", err)
	}
	return inv, nil
}

func (s *PostgresStore) GetByCode(ctx context.Context, rawCode string) (Invitation, error) {
	if s == nil || s.pool == nil {
		return Invitation{}, ErrStoreNotConfigured
	}
	code := NormalizeCode(rawCode)
	if !ValidCode(code) {
		return Invitation{}, ErrInvalidInput
	}
	row := s.pool.QueryRow(ctx, `
SELECT id, tenant_id, code, inviter_user_id, created_at, expires_at, usage_count, max_usage
FROM invitations
WHERE code=$1`, code)
	inv, err := scanInvitation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("invitation: get by code: %w", err)
	}
	return inv, nil
}

func (s *PostgresStore) GetByClientIdempotencyKey(ctx context.Context, tenantID int64, key string) (Invitation, error) {
	if s == nil || s.pool == nil {
		return Invitation{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || key == "" {
		return Invitation{}, ErrInvalidInput
	}
	row := s.pool.QueryRow(ctx, `
	SELECT id, tenant_id, code, inviter_user_id, created_at, expires_at, usage_count, max_usage
	FROM invitations
	WHERE tenant_id=$1 AND client_idempotency_key=$2`, tenantID, key)
	inv, err := scanInvitation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("invitation: get by idempotency key: %w", err)
	}
	return inv, nil
}

func (s *PostgresStore) Preview(ctx context.Context, tenantID int64, rawCode string) (InvitationPreview, error) {
	if s == nil || s.pool == nil {
		return InvitationPreview{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return InvitationPreview{}, ErrInvalidInput
	}
	code := NormalizeCode(rawCode)
	if !ValidCode(code) {
		return InvitationPreview{}, ErrInvalidInput
	}
	row := s.pool.QueryRow(ctx, `
SELECT inviter_user_id, expires_at, usage_count, max_usage
FROM invitations
WHERE tenant_id=$1 AND code=$2`, tenantID, code)
	var preview InvitationPreview
	var expiresAt sql.NullTime
	if err := row.Scan(&preview.InviterUserID, &expiresAt, &preview.UsageCount, &preview.MaxUsage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvitationPreview{}, ErrNotFound
		}
		return InvitationPreview{}, fmt.Errorf("invitation: preview: %w", err)
	}
	if expiresAt.Valid {
		t := expiresAt.Time.UTC()
		preview.ExpiresAt = &t
		if time.Now().UTC().After(t) {
			return InvitationPreview{}, ErrExpired
		}
	}
	if preview.UsageCount >= preview.MaxUsage {
		return InvitationPreview{}, ErrExhausted
	}
	return sanitizePreview(ctx, preview)
}

func (s *PostgresStore) CountTenantInvitationsSince(ctx context.Context, tenantID int64, since time.Time) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	var count int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM invitations
WHERE tenant_id=$1 AND created_at >= $2`, tenantID, since).Scan(&count); err != nil {
		return 0, fmt.Errorf("invitation: count monthly quota: %w", err)
	}
	return count, nil
}

func scanInvitation(row pgx.Row) (Invitation, error) {
	var inv Invitation
	var expiresAt sql.NullTime
	err := row.Scan(
		&inv.ID, &inv.TenantID, &inv.Code, &inv.InviterUserID,
		&inv.CreatedAt, &expiresAt, &inv.UsageCount, &inv.MaxUsage,
	)
	if err != nil {
		return Invitation{}, err
	}
	inv.Code = NormalizeCode(inv.Code)
	inv.CreatedAt = inv.CreatedAt.UTC()
	if expiresAt.Valid {
		t := expiresAt.Time.UTC()
		inv.ExpiresAt = &t
	}
	return inv, nil
}

func sanitizePreview(ctx context.Context, preview InvitationPreview) (InvitationPreview, error) {
	// 只把身份相关的安全字段交给 F-PRIV allowlist 校验；容量字段不是 PII。
	payload := map[string]any{
		"inviter_user_id": preview.InviterUserID,
	}
	if preview.ExpiresAt != nil {
		payload["expires_at"] = preview.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := privacy.DefaultRedactor().SanitizePayload(ctx, payload); err != nil {
		return InvitationPreview{}, err
	}
	return preview, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ Store = (*PostgresStore)(nil)
