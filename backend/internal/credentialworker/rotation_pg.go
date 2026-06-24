package credentialworker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRotationStore is the production RotationStore (CRED-288b/288c). It is a
// thin raw-pgx adapter on purpose: adding a new sqlc query is avoided while the
// committed sqlc output is drifted from a clean regen.
type PostgresRotationStore struct {
	pool *pgxpool.Pool
}

// NewPostgresRotationStore builds the rotation scan store over a pgx pool.
func NewPostgresRotationStore(pool *pgxpool.Pool) *PostgresRotationStore {
	return &PostgresRotationStore{pool: pool}
}

// DueForRotation returns up to limit active, non-deleted account credentials
// whose 有效上次刷新时间(COALESCE(last_refresh_at, created_at))早于 olderThan ——
// 即"距上次成功刷新已超过 maxAge"的凭据。用 COALESCE 而非裸 created_at 至关重要:
// 恢复闭环刷新成功后 last_refresh_at 会被刷成 NOW(见 SaveRefreshSuccess),凭据因此
// 立刻掉出"超期"集合,下个扫描 tick 不会再被选中——这保证扫描【幂等】、不会因为
// created_at(签发时间,永不变)而把一把"老但刚刷过"的凭据每个 tick 反复强刷、锤上游。
// 从没刷过的凭据(last_refresh_at IS NULL)回退按 created_at 计,仍能抓到静默老化。
// 最旧优先,小 per-tick 上限先清最逾期的。vendor/auth_mode 用于分类可刷新(自愈)vs 静态(仅告警)。
func (s *PostgresRotationStore) DueForRotation(ctx context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	const q = `
SELECT id, tenant_id, provider_account_id, vendor, auth_mode, COALESCE(last_refresh_at, created_at)
FROM account_credentials
WHERE state = 'active'
  AND deleted_at IS NULL
  AND COALESCE(last_refresh_at, created_at) < $1
ORDER BY COALESCE(last_refresh_at, created_at) ASC
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("credentialworker: due-for-rotation query: %w", err)
	}
	defer rows.Close()
	var out []RotationCandidate
	for rows.Next() {
		var c RotationCandidate
		if err := rows.Scan(&c.CredentialID, &c.TenantID, &c.ProviderAccountID, &c.Vendor, &c.AuthMode, &c.LastRefreshAt); err != nil {
			return nil, fmt.Errorf("credentialworker: due-for-rotation scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("credentialworker: due-for-rotation rows: %w", err)
	}
	return out, nil
}

// MarkForRefreshRecovery is the CRED-288c recovery closure for an "old but still
// refreshable" credential. It does NOT change state: the row stays 'active' so
// it keeps being served while its access token is still valid, and only pulls
// refresh_before_at down to refreshBeforeAt (now) so the existing refresh scan
// (which serves the active/refreshing_with_grace/temp_unschedulable/needs_rotation
// states with a non-null, due refresh_before_at) selects it next tick and
// re-mints the token through the audited SaveRefreshSuccess path.
//
// Safety:
//   - id+tenant+provider-account scoped and 'active'-gated, so a concurrently
//     revoked/deleted/refreshing row is a safe no-op (a revoked credential is
//     never dragged back into the refresh flow).
//   - it never advances next_attempt_at backwards: the existing backoff window
//     (next_attempt_at) is preserved, so a credential already cooling down after
//     a failed refresh is not re-attempted early and the upstream is not hammered.
//   - it never moves refresh_before_at later, so it cannot push a credential that
//     is already due further into the future.
func (s *PostgresRotationStore) MarkForRefreshRecovery(ctx context.Context, c RotationCandidate, refreshBeforeAt time.Time) error {
	if s == nil || s.pool == nil {
		return nil
	}
	const q = `
UPDATE account_credentials
SET refresh_before_at = LEAST(COALESCE(refresh_before_at, $4), $4),
    updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND provider_account_id = $3
  AND state = 'active' AND deleted_at IS NULL`
	if _, err := s.pool.Exec(ctx, q, c.CredentialID, c.TenantID, c.ProviderAccountID, refreshBeforeAt.UTC()); err != nil {
		return fmt.Errorf("credentialworker: mark-for-refresh-recovery (cred %d): %w", c.CredentialID, err)
	}
	return nil
}

// FlagNeedsRotation idempotently transitions an active credential into
// needs_rotation. It is scoped by id+tenant+provider-account and only flips an
// 'active' row, so a re-scan or a concurrently-changed row is a safe no-op.
//
// Reserved for explicit operator force-rotate / suspected-compromise: it takes a
// credential OFFLINE (needs_rotation is excluded from serving and refresh). The
// age scan does NOT route static keys here, because aging alone never
// invalidates a static API key and offlining it would brown out the account with
// no automatic path back.
func (s *PostgresRotationStore) FlagNeedsRotation(ctx context.Context, c RotationCandidate) error {
	if s == nil || s.pool == nil {
		return nil
	}
	const q = `
UPDATE account_credentials
SET state = 'needs_rotation', updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND provider_account_id = $3
  AND state = 'active' AND deleted_at IS NULL`
	if _, err := s.pool.Exec(ctx, q, c.CredentialID, c.TenantID, c.ProviderAccountID); err != nil {
		return fmt.Errorf("credentialworker: flag needs_rotation (cred %d): %w", c.CredentialID, err)
	}
	return nil
}

var _ RotationStore = (*PostgresRotationStore)(nil)
