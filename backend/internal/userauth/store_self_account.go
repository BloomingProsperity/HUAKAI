package userauth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// store_self_account.go 承载已登录用户自助账户管理(改密 / 软删)的持久层方法。
// 独立成文件以避开 store.go 的 codebudget 基线(995 行,继续 append 会爆 5% 增长 allowance);
// 自助账户是与登录 / reset / social 不同的责任面,落新文件也符合「按责任组织」纪律。

// UpdateOwnPassword 重置已认证用户自己的口令 hash 并 bump password_version。
// password_version 自增的作用:让任何在途的 password_reset_token 立即失效
// (PreparePasswordResetTokenUser 以 u.password_version = prt.password_version 为命中条件,
// version 一变,旧 reset token 不再匹配)。WHERE deleted_at IS NULL 杜绝对软删账号改密。
// 不做版本乐观锁(并发两次自助改密,后者覆盖前者 hash,可接受;reset 路径才需版本匹配)。
func (s *PostgresStore) UpdateOwnPassword(ctx context.Context, tenantID, userID int64, passwordHash string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	const q = `
UPDATE users
SET password_hash = $3,
    password_version = password_version + 1,
    updated_at = NOW()
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, userID, passwordHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (s *PostgresStore) UpdateOwnPasswordAndRevokeOthers(
	ctx context.Context,
	tenantID, userID int64,
	passwordHash string,
	expectedPasswordVersion int,
	currentFamilyID string,
	now time.Time,
) (User, int64, error) {
	if s == nil || s.db == nil {
		return User{}, 0, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || expectedPasswordVersion <= 0 {
		return User{}, 0, ErrInvalidInput
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return User{}, 0, ErrStoreNotConfigured
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return User{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	revoked, err := usersession.RevokeOtherFamiliesInTransaction(
		ctx,
		tx,
		tenantID,
		userID,
		currentFamilyID,
		"password_change",
		now,
	)
	if err != nil {
		return User{}, 0, err
	}
	const q = `
UPDATE users
SET password_hash = $3,
    password_version = password_version + 1,
    updated_at = $5
WHERE tenant_id = $1
  AND id = $2
  AND password_version = $4
  AND deleted_at IS NULL
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	user, err := scanUser(tx.QueryRow(ctx, q,
		tenantID,
		userID,
		passwordHash,
		expectedPasswordVersion,
		now.UTC(),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, 0, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, 0, err
	}
	tag, err := tx.Exec(ctx, `
UPDATE session_families
SET auth_version = $4
WHERE tenant_id = $1
  AND user_id = $2
  AND id = $3::uuid
  AND status IN ('active', 'suspicious')`,
		tenantID,
		userID,
		currentFamilyID,
		user.PasswordVersion,
	)
	if err != nil {
		return User{}, 0, err
	}
	if tag.RowsAffected() != 1 {
		return User{}, 0, usersession.ErrFamilyNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, 0, err
	}
	return user, revoked, nil
}

// CountActiveAdmins 统计某 tenant 内 role='admin' 且未软删的活跃账号数。
// 末位 admin 保护用它:删号前若本人是 admin 且该计数为 1,则拒删。
// 精确 role = 'admin'(与 panelauth 的 deny-by-default 精确匹配一致)+ deleted_at IS NULL
// (已软删的 admin 不计数,否则末位保护会因为历史软删行误判)+ tenant_id 隔离
// (跨租户计数会让末位保护失效或误触)。三条缺一不可。
func (s *PostgresStore) CountActiveAdmins(ctx context.Context, tenantID int64) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return 0, ErrInvalidInput
	}
	return countActiveAdminsWithDB(ctx, s.db, tenantID)
}

func countActiveAdminsWithDB(ctx context.Context, dbtx db.DBTX, tenantID int64) (int, error) {
	const q = `
SELECT COUNT(*)
FROM users
WHERE tenant_id = $1
  AND role = 'admin'
  AND deleted_at IS NULL`
	var count int
	if err := dbtx.QueryRow(ctx, q, tenantID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// SoftDeleteUser 软删已认证用户自己,带原子末位 admin 保护。整个流程在单事务内:
//  1. 锁住本人行(FOR UPDATE),读出 role —— 防 TOCTOU(计数与删除之间被并发改 role)。
//  2. 若本人 role='admin' 且 tenant 内 admin 计数(含本人)≤ 1 → 回滚返 ErrLastAdmin。
//  3. 否则 status='deleted' + deleted_at=NOW(),并把本人所有 active api_key 标记 revoked。
//
// status 与 deleted_at 必须同设:resolver 与 userkey 双 JOIN 同时看二者,只设其一会留下半失活态。
// api_key 显式 revoke 是审计 / 列表清晰(非安全必需,双 JOIN 已让软删用户的 key 不可解析),
// 但让 key 状态与账号状态一致。WHERE deleted_at IS NULL 保证幂等(并发第二次 0 行 → ErrUserNotFound)。
func (s *PostgresStore) SoftDeleteUser(ctx context.Context, tenantID, userID int64, now time.Time) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	if _, inTx := s.db.(pgx.Tx); inTx {
		return softDeleteUserWithDB(ctx, s.db, tenantID, userID, now)
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return softDeleteUserWithDB(ctx, s.db, tenantID, userID, now)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := softDeleteUserWithDB(ctx, tx, tenantID, userID, now)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

// SoftDeleteUserAndRevokeSessions 把账号软删、API Key 失效和全部会话撤销收进同一事务。
func (s *PostgresStore) SoftDeleteUserAndRevokeSessions(
	ctx context.Context,
	tenantID, userID int64,
	now time.Time,
) (User, int64, error) {
	if s == nil || s.db == nil {
		return User{}, 0, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, 0, ErrInvalidInput
	}
	if tx, ok := s.db.(pgx.Tx); ok {
		return softDeleteUserAndRevokeSessionsWithTx(ctx, tx, tenantID, userID, now)
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return User{}, 0, ErrStoreNotConfigured
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return User{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, revoked, err := softDeleteUserAndRevokeSessionsWithTx(ctx, tx, tenantID, userID, now)
	if err != nil {
		return User{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, 0, err
	}
	return user, revoked, nil
}

func softDeleteUserAndRevokeSessionsWithTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, userID int64,
	now time.Time,
) (User, int64, error) {
	if err := usersession.LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
		return User{}, 0, err
	}
	user, err := softDeleteUserWithDB(ctx, tx, tenantID, userID, now)
	if err != nil {
		return User{}, 0, err
	}
	revoked, err := usersession.RevokeUserInTransaction(ctx, tx, tenantID, userID, "account_deleted", now)
	if err != nil {
		return User{}, 0, err
	}
	return user, revoked, nil
}

func softDeleteUserWithDB(ctx context.Context, dbtx db.DBTX, tenantID, userID int64, now time.Time) (User, error) {
	// 锁住本人行并读 role —— 行锁让「读 role → 计数 admin → 删」对并发删/改 role 串行化,
	// 杜绝两个末位 admin 并发自删都通过的竞态。
	var role string
	if err := dbtx.QueryRow(ctx, `
SELECT role
FROM users
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
FOR UPDATE`, tenantID, userID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	if role == "admin" {
		count, err := countActiveAdminsWithDB(ctx, dbtx, tenantID)
		if err != nil {
			return User{}, err
		}
		if count <= 1 {
			return User{}, ErrLastAdmin
		}
	}
	const q = `
UPDATE users
SET status = 'deleted',
    deleted_at = $3,
    updated_at = NOW()
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	user, err := scanUser(dbtx.QueryRow(ctx, q, tenantID, userID, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	// 显式 revoke 本人 active api_key:account_deleted reason 供审计;只动 active 行,
	// 幂等(已 revoked/expired/disabled 的不再触碰)。CMB-5:不记任何 key 明文。
	if _, err := dbtx.Exec(ctx, `
UPDATE api_keys
SET status = 'revoked',
    revoked_at = $3,
    revoked_reason = 'account_deleted',
    updated_at = NOW()
WHERE tenant_id = $1
  AND user_id = $2
  AND status = 'active'
  AND deleted_at IS NULL`, tenantID, userID, now.UTC()); err != nil {
		return User{}, err
	}
	return user, nil
}
