package authcooldown

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db/authcooldowndb"
)

// PostgresPersistence 以 provider_account_auth_cooldowns 表为车道真相。失败升级、清除、读取都是
// 单条原子语句,多副本并发写同一账号由行锁串行化;刷新永久失效的硬禁在一个短事务内先锁定账号当前
// 凭据行再写入,与显式轮换串行,保证不会把旧凭据的结论写到已轮换的新凭据上。
type PostgresPersistence struct {
	pool *pgxpool.Pool
	q    *authcooldowndb.Queries
}

// NewPostgresPersistence 构造 PostgreSQL 真相后端;pool 是网关共享的连接池。
func NewPostgresPersistence(pool *pgxpool.Pool) *PostgresPersistence {
	if pool == nil {
		return nil
	}
	return &PostgresPersistence{pool: pool, q: authcooldowndb.New(pool)}
}

// Kind 实现 Persistence。
func (p *PostgresPersistence) Kind() string { return "postgres" }

// RecordFailure 实现 Persistence:去抖窗口内后端不写行并返回 no rows,这里映射为 changed=false。
func (p *PostgresPersistence) RecordFailure(ctx context.Context, accountID int64, class FailureClass, credVersion int, now time.Time, cfg Config) (PersistedState, bool, error) {
	if p == nil || p.q == nil {
		return PersistedState{}, false, errors.New("authcooldown: postgres persistence not configured")
	}
	cfg = cfg.normalized()
	row, err := p.q.RecordAuthFailure(ctx, authcooldowndb.RecordAuthFailureParams{
		ProviderAccountID:  accountID,
		Now:                pgTime(now),
		CapSeconds:         cfg.Cap.Seconds(),
		BaseSeconds:        cfg.Base.Seconds(),
		IronClad:           class == ClassIronClad,
		HardDisableStrikes: int32(cfg.HardDisableStrikeK),
		CredentialVersion:  int32(credVersion),
		FailureClass:       class.label(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PersistedState{}, false, nil
	}
	if err != nil {
		return PersistedState{}, false, fmt.Errorf("authcooldown: record failure: %w", err)
	}
	return PersistedState{
		AccountID:         accountID,
		Strike:            int(row.Strike),
		AuthUntil:         goTime(row.AuthUntil),
		HardDisabled:      row.HardDisabled,
		CredentialVersion: int(row.CredentialVersion),
		FailureClass:      class.label(),
		LastEscalatedAt:   now.UTC(),
		HardDisabledAt:    goTime(row.HardDisabledAt),
		NewlyHardDisabled: row.NewlyHardDisabled,
	}, true, nil
}

// MarkHardDisabled 实现 Persistence。事务内先以 FOR UPDATE 锁住账号当前最高版本的凭据行并读取版本:
// 高于 observedCredVersion 说明账号已轮换,这次结论属于旧凭据 → applied=false、不写行;否则在持有
// 该锁的同一事务内写入硬禁(显式轮换会更新同一凭据行,因此与本事务串行:先轮换则这里读到新版本放弃,
// 先硬禁则轮换事务随后删除刚写下的行)。observedCredVersion<=0 表示调用方不知道版本,不做守卫但仍取锁。
func (p *PostgresPersistence) MarkHardDisabled(ctx context.Context, accountID int64, observedCredVersion int, failureClass string, evidenceAt, now time.Time) (PersistedState, bool, error) {
	if p == nil || p.pool == nil {
		return PersistedState{}, false, errors.New("authcooldown: postgres persistence not configured")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return PersistedState{}, false, fmt.Errorf("authcooldown: begin hard disable: %w", err)
	}
	// 回滚用独立的短上下文:调用方上下文已超时时也要尽快释放凭据行锁,而不是等连接归还池时才回滚。
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		defer cancel()
		_ = tx.Rollback(rctx)
	}()
	q := authcooldowndb.New(tx)
	current, err := q.LockCurrentCredentialVersion(ctx, accountID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PersistedState{}, false, fmt.Errorf("authcooldown: lock credential version: %w", err)
	}
	if observedCredVersion > 0 && int(current) > observedCredVersion {
		if err := tx.Commit(ctx); err != nil {
			return PersistedState{}, false, fmt.Errorf("authcooldown: release credential lock: %w", err)
		}
		return PersistedState{}, false, nil
	}
	if evidenceAt.IsZero() {
		evidenceAt = now
	}
	if failureClass == "" {
		failureClass = failureClassRefreshPermanent
	}
	row, err := q.MarkAuthCooldownHardDisabled(ctx, authcooldowndb.MarkAuthCooldownHardDisabledParams{
		ProviderAccountID:         accountID,
		ObservedCredentialVersion: int32(observedCredVersion),
		FailureClass:              failureClass,
		Now:                       pgTime(now),
		EvidenceAt:                pgTime(evidenceAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// 两种情况都是"这次证据不生效":账号真相行不存在(没有可硬禁的对象),或行上有晚于证据的
		// 运营 resume 墓碑(补写旧本地硬禁不得撤销人工恢复)。都按 applied=false 返回,由调用方丢弃本地状态。
		if err := tx.Commit(ctx); err != nil {
			return PersistedState{}, false, fmt.Errorf("authcooldown: release credential lock: %w", err)
		}
		return PersistedState{}, false, nil
	}
	if err != nil {
		return PersistedState{}, false, fmt.Errorf("authcooldown: mark hard disabled: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PersistedState{}, false, fmt.Errorf("authcooldown: commit hard disable: %w", err)
	}
	return PersistedState{
		AccountID:         accountID,
		Strike:            int(row.Strike),
		AuthUntil:         goTime(row.AuthUntil),
		HardDisabled:      true,
		CredentialVersion: int(row.CredentialVersion),
		FailureClass:      failureClass,
		HardDisabledAt:    goTime(row.HardDisabledAt),
		NewlyHardDisabled: row.NewlyHardDisabled,
	}, true, nil
}

// Clear 实现 Persistence。运营 resume 留下 resumed_at 墓碑(账号不存在时 no rows → 无可清除状态)。
func (p *PostgresPersistence) Clear(ctx context.Context, accountID int64, hardToo bool, now time.Time) (bool, error) {
	if p == nil || p.q == nil {
		return false, errors.New("authcooldown: postgres persistence not configured")
	}
	if hardToo {
		hadRow, err := p.q.ClearAuthCooldown(ctx, authcooldowndb.ClearAuthCooldownParams{
			ProviderAccountID: accountID,
			Now:               pgTime(now),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("authcooldown: clear: %w", err)
		}
		return hadRow, nil
	}
	// 无墓碑的软退避行直接删除;带墓碑的行只清空软退避、保留墓碑。两条语句按账号 PK 互斥,合计即结果。
	deleted, err := p.q.ClearAuthCooldownSoft(ctx, accountID)
	if err != nil {
		return false, fmt.Errorf("authcooldown: clear soft: %w", err)
	}
	if deleted > 0 {
		return true, nil
	}
	kept, err := p.q.ClearAuthCooldownSoftKeepTombstone(ctx, authcooldowndb.ClearAuthCooldownSoftKeepTombstoneParams{
		ProviderAccountID: accountID,
		Now:               pgTime(now),
	})
	if err != nil {
		return false, fmt.Errorf("authcooldown: clear soft (tombstone): %w", err)
	}
	return kept > 0, nil
}

// Get 实现 Persistence。
func (p *PostgresPersistence) Get(ctx context.Context, accountID int64) (PersistedState, bool, error) {
	if p == nil || p.q == nil {
		return PersistedState{}, false, errors.New("authcooldown: postgres persistence not configured")
	}
	row, err := p.q.GetAuthCooldown(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersistedState{}, false, nil
	}
	if err != nil {
		return PersistedState{}, false, fmt.Errorf("authcooldown: get: %w", err)
	}
	// 纯墓碑 / 被成功请求清空的行对选号无状态:按"无状态"返回,与镜像重载的谓词一致,
	// 也让快照在真相无状态时回落到本地镜像(仅本副本持有的兜底仍对运营可见)。
	if !row.HardDisabled && row.Strike == 0 && !row.AuthUntil.Valid {
		return PersistedState{}, false, nil
	}
	return PersistedState{
		AccountID:         row.ProviderAccountID,
		Strike:            int(row.Strike),
		AuthUntil:         goTime(row.AuthUntil),
		HardDisabled:      row.HardDisabled,
		CredentialVersion: int(row.CredentialVersion),
		FailureClass:      row.LastFailureClass,
		LastEscalatedAt:   goTime(row.LastEscalatedAt),
		HardDisabledAt:    goTime(row.HardDisabledAt),
		ResumedAt:         goTime(row.ResumedAt),
	}, true, nil
}

// List 实现 Persistence。
func (p *PostgresPersistence) List(ctx context.Context) ([]PersistedState, error) {
	if p == nil || p.q == nil {
		return nil, errors.New("authcooldown: postgres persistence not configured")
	}
	rows, err := p.q.ListAuthCooldowns(ctx)
	if err != nil {
		return nil, fmt.Errorf("authcooldown: list: %w", err)
	}
	out := make([]PersistedState, 0, len(rows))
	for _, row := range rows {
		out = append(out, PersistedState{
			AccountID:         row.ProviderAccountID,
			Strike:            int(row.Strike),
			AuthUntil:         goTime(row.AuthUntil),
			HardDisabled:      row.HardDisabled,
			CredentialVersion: int(row.CredentialVersion),
			FailureClass:      row.LastFailureClass,
			LastEscalatedAt:   goTime(row.LastEscalatedAt),
			HardDisabledAt:    goTime(row.HardDisabledAt),
			ResumedAt:         goTime(row.ResumedAt),
		})
	}
	return out, nil
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func goTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
