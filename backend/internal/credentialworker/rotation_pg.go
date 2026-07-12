package credentialworker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRotationStore 是生产用的 RotationStore(CRED-288b/288c)。它刻意做成一个
// 薄薄的 raw-pgx adapter:在已提交的 sqlc 输出相对干净重生成已漂移期间,避免新增
// 一条 sqlc query。
type PostgresRotationStore struct {
	pool *pgxpool.Pool
}

// NewPostgresRotationStore 在一个 pgx pool 之上构建 rotation 扫描 store。
func NewPostgresRotationStore(pool *pgxpool.Pool) *PostgresRotationStore {
	return &PostgresRotationStore{pool: pool}
}

// DueForRotation 返回至多 limit 条 active、未删除的 account credential,这些凭据的
// 有效上次刷新时间(COALESCE(last_refresh_at, created_at))早于 olderThan ——
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

// MarkForRefreshRecovery 是针对"年龄大但仍可刷新"凭据的 CRED-288c 恢复闭环。它不
// 改变 state:该行保持 'active',因此在其 access token 仍有效期间继续被服务;它只把
// refresh_before_at 拉低到 refreshBeforeAt(当前时刻),使现有的 refresh 扫描(它服务
// active/refreshing_with_grace/temp_unschedulable/needs_rotation 这些状态、且
// refresh_before_at 非空且到期的行)在下一个 tick 挑中它,并通过经过审计的
// SaveRefreshSuccess 路径重新铸造 token。
//
// 安全性:
//   - 以 id+tenant+provider-account 限定作用域并以 'active' 为门控,因此一个并发被
//     revoke/delete/refreshing 的行是一次安全的 no-op(一个被撤销的凭据绝不会被拖回
//     refresh 流)。
//   - 它绝不把 next_attempt_at 往回拨:现有的退避窗口(next_attempt_at)被保留,因此
//     一个在刷新失败后已处于冷却中的凭据不会被提前重试,上游也不会被锤。
//   - 它绝不把 refresh_before_at 往后移,因此它不会把一个已经到期的凭据进一步推到
//     未来。
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

// FlagNeedsRotation 幂等地把一个 active 凭据转入 needs_rotation。它以
// id+tenant+provider-account 限定作用域,且只翻转 'active' 行,因此一次重扫或一个
// 并发被改动的行是一次安全的 no-op。
//
// 保留给显式的 operator force-rotate / 疑似泄露场景:它会把一个凭据下线
// (needs_rotation 被排除在服务与刷新之外)。年龄扫描不会把静态 key 路由到这里,
// 因为年龄本身绝不会使一个静态 API key 失效,而把它下线会使账号陷入无自动回路的
// 降级。
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
