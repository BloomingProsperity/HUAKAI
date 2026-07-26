//go:build integration_pg

package userauth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChangeOwnPassword 真库:改密后 (a) 新密 VerifyPassword 通过、旧密失败 (b) password_version 自增 1
// (c) 旧 password_reset_token(password_version=旧值)在改密后 PreparePasswordReset 不再命中(version 已变)。
// MUTATION: store UpdateOwnPassword 不 bump password_version → (b) 断言红 + (c) 旧 reset token 仍命中 → 红。
// 判别 fixture:旧/新明文不同,断言 hash 行确实变(非 no-op UPDATE)。
func TestPGChangeOwnPasswordBumpsVersionAndInvalidatesResetToken(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfpw-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	oldHash, err := HashPassword("old-secret-pw", svc.PasswordPolicy)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "selfpw-" + suffix + "@example.test", DisplayName: "Self PW",
		PasswordHash: oldHash, EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	versionBefore := readPasswordVersion(t, ctx, pool, tenantID, user.ID)

	// 旧 reset token,绑定改密前的 password_version(模拟「忘密申请已发出但未用」)。
	challenge, err := NewTokenChallenge(tenantID, user.ID, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("NewTokenChallenge: %v", err)
	}
	if err := store.CreatePasswordResetToken(ctx, challenge, versionBefore); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	if _, err := svc.ChangeOwnPassword(ctx, tenantID, user.ID, "old-secret-pw", "fresh-secret-pw"); err != nil {
		t.Fatalf("ChangeOwnPassword: %v", err)
	}

	// (a) 新密验过、旧密验不过。
	storedHash := readPasswordHash(t, ctx, pool, tenantID, user.ID)
	if storedHash == oldHash {
		t.Fatalf("password_hash unchanged (no-op UPDATE); MUTATION: skipping SET password_hash leaves old hash")
	}
	if ok, _ := VerifyPassword(storedHash, "fresh-secret-pw"); !ok {
		t.Fatalf("new password does not verify against stored hash")
	}
	if ok, _ := VerifyPassword(storedHash, "old-secret-pw"); ok {
		t.Fatalf("old password still verifies after change")
	}
	// (b) password_version 自增 1。
	if got := readPasswordVersion(t, ctx, pool, tenantID, user.ID); got != versionBefore+1 {
		t.Fatalf("password_version=%d want %d (before+1); MUTATION: not bumping version leaves %d", got, versionBefore+1, versionBefore)
	}
	// (c) 旧 reset token 不再命中(version 已变,PreparePasswordResetTokenUser 的
	//     u.password_version = prt.password_version 不满足)。
	if _, err := store.PreparePasswordResetTokenUser(ctx, tenantID, challenge.TokenHash, time.Now()); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("stale reset token Prepare err=%v want ErrTokenInvalid; MUTATION: not bumping password_version keeps the token valid", err)
	}
}

// 自助改密真库原子性：
//  1. 当前会话族及令牌保留；
//  2. 其他会话族、短令牌和刷新令牌全部撤销；
//  3. 口令与 password_version 同一事务提交。
//
// MUTATION：删掉会话撤销、误撤当前会话，或把改密与撤会话拆成两个事务，都会由明确状态断言抓住。
func TestPGChangeOwnPasswordAndRevokeOthersCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfpw-session-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	oldHash, err := HashPassword("old-session-secret", svc.PasswordPolicy)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "selfpw-session-" + suffix + "@example.test", DisplayName: "Self PW Session",
		PasswordHash: oldHash, EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	current := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "current-"+suffix)
	other := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "other-"+suffix)
	versionBefore := readPasswordVersion(t, ctx, pool, tenantID, user.ID)

	updated, revoked, err := svc.ChangeOwnPasswordAndRevokeOthers(
		ctx,
		tenantID,
		user.ID,
		"old-session-secret",
		"new-session-secret",
		current.familyID.String(),
	)
	if err != nil {
		t.Fatalf("ChangeOwnPasswordAndRevokeOthers: %v", err)
	}
	if updated.PasswordVersion != versionBefore+1 {
		t.Fatalf("returned password_version=%d want %d", updated.PasswordVersion, versionBefore+1)
	}
	if revoked != 1 {
		t.Fatalf("revoked families=%d want 1", revoked)
	}
	storedHash := readPasswordHash(t, ctx, pool, tenantID, user.ID)
	if ok, _ := VerifyPassword(storedHash, "new-session-secret"); !ok {
		t.Fatalf("new password does not verify after atomic commit")
	}
	assertSelfAccountSessionState(t, ctx, pool, current, "active", "active", false)
	if got := readSelfAccountSessionAuthVersion(t, ctx, pool, current); got != updated.PasswordVersion {
		t.Fatalf(
			"current session auth_version=%d want updated password_version=%d; "+
				"keeping the family active without advancing its security version would leave a stale session",
			got,
			updated.PasswordVersion,
		)
	}
	assertSelfAccountSessionState(t, ctx, pool, other, "revoked", "revoked", true)
}

// 故障注入让「撤销其他会话族」失败，证明密码、版本和全部会话状态一起回滚。
// MUTATION：若实现先提交密码再尽力撤会话，本测试会看到新 hash 或递增版本并变红。
func TestPGChangeOwnPasswordAndRevokeOthersRollsBackOnSessionFailure(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfpw-rollback-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	oldHash, err := HashPassword("rollback-old-secret", svc.PasswordPolicy)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "selfpw-rollback-" + suffix + "@example.test", DisplayName: "Self PW Rollback",
		PasswordHash: oldHash, EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	current := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "current-rollback-"+suffix)
	other := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "other-rollback-"+suffix)
	versionBefore := readPasswordVersion(t, ctx, pool, tenantID, user.ID)

	installSelfAccountSessionRevokeFailure(t, ctx, pool)
	if _, _, err := svc.ChangeOwnPasswordAndRevokeOthers(
		ctx,
		tenantID,
		user.ID,
		"rollback-old-secret",
		"rollback-new-secret",
		current.familyID.String(),
	); err == nil {
		t.Fatalf("ChangeOwnPasswordAndRevokeOthers err=nil want injected session failure")
	}

	if got := readPasswordHash(t, ctx, pool, tenantID, user.ID); got != oldHash {
		t.Fatalf("password_hash changed after rollback")
	}
	if got := readPasswordVersion(t, ctx, pool, tenantID, user.ID); got != versionBefore {
		t.Fatalf("password_version=%d want unchanged %d", got, versionBefore)
	}
	assertSelfAccountSessionState(t, ctx, pool, current, "active", "active", false)
	assertSelfAccountSessionState(t, ctx, pool, other, "active", "active", false)
}

// SoftDeleteSelf 真库:删后 (a) GetUserByID 返 ErrUserNotFound(deleted_at IS NULL 过滤)
// (b) status='deleted' 且 deleted_at IS NOT NULL(二者必须同设——resolver 同时看,半设留漏洞)
// (c) 本人 active api_key 变 revoked(同事务)。
// MUTATION: SoftDeleteUser 只 SET deleted_at 不 SET status='deleted'(或反)→ (b) 同设断言红;
// 不 revoke api_key → (c) 断言红。
func TestPGSoftDeleteSelfFlipsStatusDeletedAtAndRevokesKeys(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfdel-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "selfdel-" + suffix + "@example.test", DisplayName: "Self Del",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	keyID := seedSelfAccountAPIKey(t, ctx, pool, tenantID, user.ID, "active")
	session := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "self-delete-"+suffix)

	if _, revoked, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, user.ID); err != nil {
		t.Fatalf("SoftDeleteSelfAndRevokeSessions: %v", err)
	} else if revoked != 1 {
		t.Fatalf("sessions revoked=%d want 1", revoked)
	}

	// (a) 软删后按 ID 读不到(deleted_at IS NULL 过滤)。
	if _, err := store.GetUserByID(ctx, tenantID, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetUserByID after soft delete err=%v want ErrUserNotFound", err)
	}
	// (b) status 与 deleted_at 同设。
	status, deletedAtSet := readUserStatusAndDeleted(t, ctx, pool, tenantID, user.ID)
	if status != "deleted" || !deletedAtSet {
		t.Fatalf("after delete status=%q deleted_at_set=%v want deleted/true; MUTATION: setting only one leaves a half-deactivated row", status, deletedAtSet)
	}
	// (c) active api_key 被同事务 revoke。
	if got := readAPIKeyStatus(t, ctx, pool, keyID); got != "revoked" {
		t.Fatalf("api_key status=%q want revoked; MUTATION: skipping the api_keys UPDATE leaves the key active", got)
	}
	assertSelfAccountSessionState(t, ctx, pool, session, "revoked", "revoked", true)

	// 幂等:并发第二次删 0 行 → ErrUserNotFound(已被 deleted_at IS NULL 过滤)。
	if _, _, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("second SoftDeleteSelfAndRevokeSessions err=%v want ErrUserNotFound (idempotent)", err)
	}
}

// 会话撤销失败时，账号、API Key 和会话必须全部保持原状。
func TestPGSoftDeleteSelfRollsBackOnSessionFailure(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfdel-rollback-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "selfdel-rollback-" + suffix + "@example.test", DisplayName: "Self Del Rollback",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	keyID := seedSelfAccountAPIKey(t, ctx, pool, tenantID, user.ID, "active")
	session := seedSelfAccountSessionFamily(t, ctx, pool, tenantID, user.ID, "self-delete-rollback-"+suffix)
	installSelfAccountSessionRevokeFailure(t, ctx, pool)

	if _, _, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, user.ID); err == nil {
		t.Fatalf("SoftDeleteSelfAndRevokeSessions err=nil want injected session failure")
	}
	status, deleted := readUserStatusAndDeleted(t, ctx, pool, tenantID, user.ID)
	if status != "active" || deleted {
		t.Fatalf("user status=%q deleted=%v want active/false after rollback", status, deleted)
	}
	if got := readAPIKeyStatus(t, ctx, pool, keyID); got != "active" {
		t.Fatalf("api_key status=%q want active after rollback", got)
	}
	assertSelfAccountSessionState(t, ctx, pool, session, "active", "active", false)
}

// 末位 admin 真库:tenant 仅 1 个 role='admin' 自删 → ErrLastAdmin(0 行删,user 仍活);
// 建第 2 个 admin 后删第 1 个 → 成功。
// 判别 fixture:第 2 admin 设 deleted_at(已软删 admin)时仍触发末位保护——证明 CountActiveAdmins
// 带 deleted_at IS NULL。
// MUTATION: CountActiveAdmins 漏 role='admin' 或漏 deleted_at IS NULL → 末位场景误放行 → 红。
func TestPGSoftDeleteSelfLastAdminProtection(t *testing.T) {
	ctx := context.Background()
	pool := openSelfAccountPool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := selfAccountTestService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedSelfAccountTenant(t, ctx, pool, "selfadmin-"+suffix)
	t.Cleanup(func() { cleanupSelfAccountTenant(t, ctx, pool, tenantID) })

	admin1 := seedSelfAccountAdmin(t, ctx, pool, tenantID, "admin1-"+suffix, false)

	// 末位 admin(唯一一个)自删 → 拒绝。
	if _, _, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, admin1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last-admin self delete err=%v want ErrLastAdmin", err)
	}
	if status, deletedAtSet := readUserStatusAndDeleted(t, ctx, pool, tenantID, admin1); status == "deleted" || deletedAtSet {
		t.Fatalf("rejected delete still mutated row status=%q deleted=%v; protection must roll back", status, deletedAtSet)
	}

	// 加一个「已软删」的第 2 admin:不计入活跃 admin → 第 1 admin 仍是末位 → 仍拒。
	seedSelfAccountAdmin(t, ctx, pool, tenantID, "admin2soft-"+suffix, true)
	if _, _, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, admin1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("with only a soft-deleted 2nd admin err=%v want ErrLastAdmin; MUTATION: CountActiveAdmins missing deleted_at IS NULL counts the soft-deleted admin and wrongly allows delete", err)
	}

	// 加一个活跃第 2 admin → 第 1 admin 不再末位 → 可删。
	seedSelfAccountAdmin(t, ctx, pool, tenantID, "admin2live-"+suffix, false)
	if _, _, err := svc.SoftDeleteSelfAndRevokeSessions(ctx, tenantID, admin1); err != nil {
		t.Fatalf("non-last admin self delete: %v", err)
	}
	if status, deletedAtSet := readUserStatusAndDeleted(t, ctx, pool, tenantID, admin1); status != "deleted" || !deletedAtSet {
		t.Fatalf("non-last admin not soft-deleted status=%q deleted=%v", status, deletedAtSet)
	}
}

// --- 辅助工具 ----------------------------------------------------------------

func selfAccountTestService(store Store) *Service {
	svc := NewService(store)
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	return svc
}

func openSelfAccountPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	return openUserAuthProfilePool(t, ctx)
}

func seedSelfAccountTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	return seedUserAuthProfileTenant(t, ctx, pool, name)
}

func seedSelfAccountAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, email string, softDeleted bool) int64 {
	t.Helper()
	var id int64
	if softDeleted {
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (tenant_id, email, display_name, status, role, deleted_at)
			 VALUES ($1, $2, $3, 'deleted', 'admin', NOW()) RETURNING id`,
			tenantID, email+"@example.test", email).Scan(&id); err != nil {
			t.Fatalf("insert soft-deleted admin: %v", err)
		}
		return id
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, display_name, status, role)
		 VALUES ($1, $2, $3, 'active', 'admin') RETURNING id`,
		tenantID, email+"@example.test", email).Scan(&id); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	return id
}

func seedSelfAccountAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, status string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, 'self-account-test', 'bcrypt-test-hash', 'hk_live_testpref', $3) RETURNING id`,
		tenantID, userID, status).Scan(&id); err != nil {
		t.Fatalf("insert api_key: %v", err)
	}
	return id
}

type selfAccountSessionFixture struct {
	tenantID     int64
	familyID     uuid.UUID
	refreshToken uuid.UUID
	sessionToken uuid.UUID
}

func seedSelfAccountSessionFamily(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, userID int64,
	tokenSeed string,
) selfAccountSessionFixture {
	t.Helper()
	fixture := selfAccountSessionFixture{
		tenantID:     tenantID,
		familyID:     uuid.New(),
		refreshToken: uuid.New(),
		sessionToken: uuid.New(),
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO session_families (id, tenant_id, user_id, status, generation)
VALUES ($1, $2, $3, 'active', 1)`,
		fixture.familyID, tenantID, userID); err != nil {
		t.Fatalf("insert session family: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at)
VALUES ($1, $2, $3, $4, 1, 'active', NOW() + INTERVAL '1 day')`,
		fixture.refreshToken, tenantID, fixture.familyID, []byte("refresh-"+tokenSeed)); err != nil {
		t.Fatalf("insert refresh token: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at)
VALUES ($1, $2, $3, $4, 1, NOW() + INTERVAL '1 hour')`,
		fixture.sessionToken, tenantID, fixture.familyID, []byte("session-"+tokenSeed)); err != nil {
		t.Fatalf("insert session token: %v", err)
	}
	return fixture
}

func assertSelfAccountSessionState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture selfAccountSessionFixture,
	wantFamilyStatus, wantRefreshStatus string,
	wantSessionRevoked bool,
) {
	t.Helper()
	var familyStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM session_families WHERE tenant_id=$1 AND id=$2`,
		fixture.tenantID, fixture.familyID).Scan(&familyStatus); err != nil {
		t.Fatalf("read family status: %v", err)
	}
	var refreshStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refresh_tokens WHERE tenant_id=$1 AND id=$2`,
		fixture.tenantID, fixture.refreshToken).Scan(&refreshStatus); err != nil {
		t.Fatalf("read refresh status: %v", err)
	}
	var sessionRevoked bool
	if err := pool.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL FROM session_tokens WHERE tenant_id=$1 AND id=$2`,
		fixture.tenantID, fixture.sessionToken).Scan(&sessionRevoked); err != nil {
		t.Fatalf("read session token state: %v", err)
	}
	if familyStatus != wantFamilyStatus || refreshStatus != wantRefreshStatus || sessionRevoked != wantSessionRevoked {
		t.Fatalf(
			"session state family=%q refresh=%q session_revoked=%v want %q/%q/%v",
			familyStatus, refreshStatus, sessionRevoked,
			wantFamilyStatus, wantRefreshStatus, wantSessionRevoked,
		)
	}
}

func readSelfAccountSessionAuthVersion(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture selfAccountSessionFixture,
) int {
	t.Helper()
	var version int
	if err := pool.QueryRow(ctx,
		`SELECT auth_version FROM session_families WHERE tenant_id=$1 AND id=$2`,
		fixture.tenantID, fixture.familyID).Scan(&version); err != nil {
		t.Fatalf("read session auth version: %v", err)
	}
	return version
}

func installSelfAccountSessionRevokeFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION huakai_test_fail_self_account_session_revoke()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status IN ('active', 'suspicious') AND NEW.status = 'revoked' THEN
        RAISE EXCEPTION 'injected self-account session revoke failure';
    END IF;
    RETURN NEW;
END;
$$`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
CREATE TRIGGER huakai_test_fail_self_account_session_revoke
BEFORE UPDATE ON session_families
FOR EACH ROW
EXECUTE FUNCTION huakai_test_fail_self_account_session_revoke()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS huakai_test_fail_self_account_session_revoke ON session_families`); err != nil {
			t.Errorf("drop failure trigger: %v", err)
		}
		if _, err := pool.Exec(ctx, `DROP FUNCTION IF EXISTS huakai_test_fail_self_account_session_revoke()`); err != nil {
			t.Errorf("drop failure function: %v", err)
		}
	})
}

func readPasswordVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) int {
	t.Helper()
	var v int
	if err := pool.QueryRow(ctx, `SELECT password_version FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&v); err != nil {
		t.Fatalf("read password_version: %v", err)
	}
	return v
}

func readPasswordHash(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) string {
	t.Helper()
	var h string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&h); err != nil {
		t.Fatalf("read password_hash: %v", err)
	}
	return h
}

func readUserStatusAndDeleted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) (string, bool) {
	t.Helper()
	var status string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, deleted_at FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("read user status/deleted_at: %v", err)
	}
	return status, deletedAt != nil
}

func readAPIKeyStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, keyID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, keyID).Scan(&status); err != nil {
		t.Fatalf("read api_key status: %v", err)
	}
	return status
}

func cleanupSelfAccountTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup refresh_tokens: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM session_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup session_tokens: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM session_families WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup session_families: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup api_keys: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup password_reset_tokens: %v", err)
	}
	cleanupUserAuthProfileTenant(t, ctx, pool, tenantID)
}
