//go:build integration_pg

package userauth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

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

	if _, err := svc.SoftDeleteSelf(ctx, tenantID, user.ID); err != nil {
		t.Fatalf("SoftDeleteSelf: %v", err)
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

	// 幂等:并发第二次删 0 行 → ErrUserNotFound(已被 deleted_at IS NULL 过滤)。
	if _, err := svc.SoftDeleteSelf(ctx, tenantID, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("second SoftDeleteSelf err=%v want ErrUserNotFound (idempotent)", err)
	}
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
	if _, err := svc.SoftDeleteSelf(ctx, tenantID, admin1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last-admin self delete err=%v want ErrLastAdmin", err)
	}
	if status, deletedAtSet := readUserStatusAndDeleted(t, ctx, pool, tenantID, admin1); status == "deleted" || deletedAtSet {
		t.Fatalf("rejected delete still mutated row status=%q deleted=%v; protection must roll back", status, deletedAtSet)
	}

	// 加一个「已软删」的第 2 admin:不计入活跃 admin → 第 1 admin 仍是末位 → 仍拒。
	seedSelfAccountAdmin(t, ctx, pool, tenantID, "admin2soft-"+suffix, true)
	if _, err := svc.SoftDeleteSelf(ctx, tenantID, admin1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("with only a soft-deleted 2nd admin err=%v want ErrLastAdmin; MUTATION: CountActiveAdmins missing deleted_at IS NULL counts the soft-deleted admin and wrongly allows delete", err)
	}

	// 加一个活跃第 2 admin → 第 1 admin 不再末位 → 可删。
	seedSelfAccountAdmin(t, ctx, pool, tenantID, "admin2live-"+suffix, false)
	if _, err := svc.SoftDeleteSelf(ctx, tenantID, admin1); err != nil {
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
	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup api_keys: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup password_reset_tokens: %v", err)
	}
	cleanupUserAuthProfileTenant(t, ctx, pool, tenantID)
}
