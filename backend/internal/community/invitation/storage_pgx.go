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
	// 锁键整串在 Go 侧拼好后作单个 text 参数传入：原先 `'invitation_quota:' || $1::text` 把 int64
	// tenant_id 声明成 text 参数，pgx 扩展协议下无法把 int64 编码成 text(OID 25)→ "cannot find
	// encode plan",导致所有邀请/推广码生成 503。改成传一个真 text 参数即可,哈希值不变。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("invitation_quota:%d", rec.TenantID)); err != nil {
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
	// QuotaExempt 的行（自荐码）完全跳过活动上限：用户物化自己的稳定码
	// 绝不能被共享的单租户每月活动配额拦截。下面的计数也排除了自荐行，
	// 使它们永远不会挤占活动预算。
	if !rec.QuotaExempt {
		var count int
		if err := tx.QueryRow(ctx, `
	SELECT COUNT(*)
FROM invitations
WHERE tenant_id=$1 AND created_at >= $2
  AND (client_idempotency_key IS NULL OR client_idempotency_key NOT LIKE $3)`,
			rec.TenantID, monthStartUTC(rec.CreatedAt), SelfReferralIdempotencyPrefix+"%").Scan(&count); err != nil {
			return Invitation{}, fmt.Errorf("invitation: quota recheck: %w", err)
		}
		if count >= MonthlyTenantQuota {
			return Invitation{}, ErrQuotaExceeded
		}
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
	// 排除自荐码：它们是免配额的身份行，不得消耗单租户共享的每月活动预算。
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM invitations
WHERE tenant_id=$1 AND created_at >= $2
  AND (client_idempotency_key IS NULL OR client_idempotency_key NOT LIKE $3)`,
		tenantID, since, SelfReferralIdempotencyPrefix+"%").Scan(&count); err != nil {
		return 0, fmt.Errorf("invitation: count monthly quota: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) GetReferralSummary(ctx context.Context, tenantID, referrerUserID int64) (ReferralSummary, error) {
	if s == nil || s.pool == nil {
		return ReferralSummary{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || referrerUserID <= 0 {
		return ReferralSummary{}, ErrInvalidInput
	}
	var summary ReferralSummary
	if err := s.pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE r.status='qualified')::bigint,
	COUNT(*) FILTER (WHERE r.status='rewarded')::bigint,
	(COALESCE(SUM(rr.amount_usd_micros), 0)::bigint / 10000)::bigint
FROM referrals r
LEFT JOIN referral_rewards rr
  ON rr.tenant_id=r.tenant_id AND rr.referral_id=r.id
WHERE r.tenant_id=$1 AND r.referrer_user_id=$2`,
		tenantID, referrerUserID,
	).Scan(&summary.QualifiedCount, &summary.RewardedCount, &summary.RewardsEarnedCents); err != nil {
		return ReferralSummary{}, fmt.Errorf("invitation: referral summary: %w", err)
	}
	return summary, nil
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
