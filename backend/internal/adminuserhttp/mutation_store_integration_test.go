//go:build integration_pg

package adminuserhttp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/passkey"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestAdminUserMutationsAndLogsShareOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserMutationStore(pool)

	groupUser := f.seedUser("tx-group", "active", "user", "0.00000000")
	remarkUser := f.seedUser("tx-remark", "active", "user", "0.00000000")
	statusUser := f.seedUser("tx-status", "active", "user", "0.00000000")
	deleteUser := f.seedUser("tx-delete", "active", "user", "0.00000000")
	twoFAUser := f.seedUser("tx-2fa", "active", "user", "0.00000000")
	passkeyUser := f.seedUser("tx-passkey", "active", "user", "0.00000000")
	socialUser, _ := f.seedPasswordUser("tx-social", "correct-secret")
	statusSession := seedAdminMutationSession(t, ctx, f, statusUser, "status")
	deleteSession := seedAdminMutationSession(t, ctx, f, deleteUser, "delete")
	twoFASession := seedAdminMutationSession(t, ctx, f, twoFAUser, "twofa")
	passkeySession := seedAdminMutationSession(t, ctx, f, passkeyUser, "passkey")
	socialSession := seedAdminMutationSession(t, ctx, f, socialUser, "social")

	f.seedTwoFASetting(twoFAUser, true)
	if _, err := pool.Exec(ctx, `
INSERT INTO two_factor_backup_codes(tenant_id,user_id,code_hash)
VALUES($1,$2,$3)`, f.tenantID, twoFAUser, []byte("backup-"+f.suffix)); err != nil {
		t.Fatalf("插入 2FA 备用码: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO passkey_credentials(tenant_id,user_id,credential_id,public_key)
VALUES($1,$2,$3,$4)`,
		f.tenantID, passkeyUser, []byte("credential-"+f.suffix), []byte("public-key"),
	); err != nil {
		t.Fatalf("插入 Passkey: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO social_identity_links(tenant_id,user_id,provider,subject,email_verified)
VALUES($1,$2,$3,$4,true)`,
		f.tenantID, socialUser, userauth.SocialProviderGitHub, "subject-"+f.suffix,
	); err != nil {
		t.Fatalf("插入社交身份: %v", err)
	}

	invalidAudit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: "invalid_role"}
	assertMutationFails(t, store.SetUserGroupWithAudit(ctx, f.tenantID, groupUser, "premium", invalidAudit))
	assertMutationFails(t, store.SetUserRemarkWithAudit(ctx, f.tenantID, remarkUser, "private note", invalidAudit))
	_, err := store.SetUserStatusWithAudit(ctx, f.tenantID, statusUser, "disabled", "test", invalidAudit)
	assertMutationFails(t, err)
	_, err = store.SoftDeleteUserWithAudit(ctx, f.tenantID, deleteUser, invalidAudit)
	assertMutationFails(t, err)
	_, err = store.ForceDisableTwoFAWithAudit(ctx, f.tenantID, twoFAUser, invalidAudit)
	assertMutationFails(t, err)
	if _, _, err := store.ResetPasskeysWithAudit(ctx, f.tenantID, passkeyUser, invalidAudit); err == nil {
		t.Fatal("日志写入失败时 Passkey 清理必须失败")
	}
	if _, _, err := store.UnlinkSocialIdentityWithAudit(
		ctx, f.tenantID, socialUser, userauth.SocialProviderGitHub, invalidAudit,
	); err == nil {
		t.Fatal("日志写入失败时社交身份解绑必须失败")
	}

	assertAdminUserMutationState(t, ctx, f, groupUser, "active", "default", "", false)
	assertAdminUserMutationState(t, ctx, f, remarkUser, "active", "default", "", false)
	assertAdminUserMutationState(t, ctx, f, statusUser, "active", "default", "", false)
	assertAdminUserMutationState(t, ctx, f, deleteUser, "active", "default", "", false)
	assertTwoFAEnabled(t, ctx, f, twoFAUser, true)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM passkey_credentials WHERE tenant_id=$1 AND user_id=$2`, passkeyUser, 1)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM social_identity_links WHERE tenant_id=$1 AND user_id=$2`, socialUser, 1)
	for _, familyID := range []uuid.UUID{statusSession, deleteSession, twoFASession, passkeySession, socialSession} {
		assertAdminMutationSession(t, ctx, f, familyID, false)
	}

	validAudit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: admin.RoleTenantOperator}
	if err := store.SetUserGroupWithAudit(ctx, f.tenantID, groupUser, "premium", validAudit); err != nil {
		t.Fatalf("提交用户分组与日志: %v", err)
	}
	if err := store.SetUserRemarkWithAudit(ctx, f.tenantID, remarkUser, "private note", validAudit); err != nil {
		t.Fatalf("提交用户备注与日志: %v", err)
	}
	if revoked, err := store.SetUserStatusWithAudit(ctx, f.tenantID, statusUser, "disabled", "test", validAudit); err != nil || revoked != 1 {
		t.Fatalf("提交用户状态与日志: %v", err)
	}
	if revoked, err := store.SoftDeleteUserWithAudit(ctx, f.tenantID, deleteUser, validAudit); err != nil || revoked != 1 {
		t.Fatalf("提交用户删除与日志: %v", err)
	}
	if revoked, err := store.ForceDisableTwoFAWithAudit(ctx, f.tenantID, twoFAUser, validAudit); err != nil || revoked != 1 {
		t.Fatalf("提交 2FA 清理与日志: %v", err)
	}
	cleared, revoked, err := store.ResetPasskeysWithAudit(ctx, f.tenantID, passkeyUser, validAudit)
	if err != nil || cleared != 1 || revoked != 1 {
		t.Fatalf("提交 Passkey 清理与日志: cleared=%d revoked=%d err=%v", cleared, revoked, err)
	}
	unlinked, revoked, err := store.UnlinkSocialIdentityWithAudit(
		ctx, f.tenantID, socialUser, userauth.SocialProviderGitHub, validAudit,
	)
	if err != nil || !unlinked || revoked != 1 {
		t.Fatalf("提交社交身份解绑与日志: unlinked=%v revoked=%d err=%v", unlinked, revoked, err)
	}

	assertAdminUserMutationState(t, ctx, f, groupUser, "active", "premium", "", false)
	assertAdminUserMutationState(t, ctx, f, remarkUser, "active", "default", "private note", false)
	assertAdminUserMutationState(t, ctx, f, statusUser, "disabled", "default", "", false)
	assertAdminUserMutationState(t, ctx, f, deleteUser, "deleted", "default", "", true)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM two_factor_settings WHERE tenant_id=$1 AND user_id=$2`, twoFAUser, 0)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM two_factor_backup_codes WHERE tenant_id=$1 AND user_id=$2`, twoFAUser, 0)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM passkey_credentials WHERE tenant_id=$1 AND user_id=$2`, passkeyUser, 0)
	assertRowCount(t, ctx, f, `SELECT count(*) FROM social_identity_links WHERE tenant_id=$1 AND user_id=$2`, socialUser, 0)
	for _, familyID := range []uuid.UUID{statusSession, deleteSession, twoFASession, passkeySession, socialSession} {
		assertAdminMutationSession(t, ctx, f, familyID, true)
	}

	var logCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id=$1
  AND actor_id='admin_token:305'
  AND action IN (
      'set_user_group','set_user_remark','set_user_status','delete_user',
      'force_disable_2fa','reset_passkey','unlink_social_identity'
  )`, f.tenantID).Scan(&logCount); err != nil {
		t.Fatalf("读取用户操作日志: %v", err)
	}
	if logCount != 7 {
		t.Fatalf("七项业务写入必须各有一条同事务日志，得到 %d", logCount)
	}
}

func TestAdminSecurityRecoveryRevokesSessionsEvenWhenFactorAlreadyAbsent(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserMutationStore(pool)
	twoFAUser := f.seedUser("tx-2fa-noop", "active", "user", "0.00000000")
	passkeyUser := f.seedUser("tx-passkey-noop", "active", "user", "0.00000000")
	twoFASession := seedAdminMutationSession(t, ctx, f, twoFAUser, "twofa-noop")
	passkeySession := seedAdminMutationSession(t, ctx, f, passkeyUser, "passkey-noop")
	audit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: admin.RoleTenantOperator}

	if revoked, err := store.ForceDisableTwoFAWithAudit(ctx, f.tenantID, twoFAUser, audit); err != nil || revoked != 1 {
		t.Fatalf("无 2FA 恢复 revoked=%d err=%v，期望仍撤一组会话", revoked, err)
	}
	if cleared, revoked, err := store.ResetPasskeysWithAudit(ctx, f.tenantID, passkeyUser, audit); err != nil || cleared != 0 || revoked != 1 {
		t.Fatalf("无 Passkey 恢复 cleared=%d revoked=%d err=%v", cleared, revoked, err)
	}
	assertAdminMutationSession(t, ctx, f, twoFASession, true)
	assertAdminMutationSession(t, ctx, f, passkeySession, true)
}

func TestAdminSecurityRecoveryRejectsStaleAuthenticatedSessionCreation(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserMutationStore(pool)
	userID := f.seedUser("tx-auth-version", "active", "user", "0.00000000")

	var oldVersion int
	if err := pool.QueryRow(ctx, `
SELECT password_version
FROM users
WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&oldVersion); err != nil {
		t.Fatalf("读取旧认证版本: %v", err)
	}
	audit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: admin.RoleTenantOperator}
	if cleared, revoked, err := store.ResetPasskeysWithAudit(
		ctx, f.tenantID, userID, audit,
	); err != nil || cleared != 0 || revoked != 0 {
		t.Fatalf("执行无存量 Passkey 的安全恢复: cleared=%d revoked=%d err=%v", cleared, revoked, err)
	}

	sessions := usersession.NewService(usersession.NewPostgresStore(pool))
	sessions.SigningKey = []byte(strings.Repeat("s", 32))
	sessions.Now = func() time.Time { return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC) }
	if _, err := sessions.Create(ctx, usersession.CreateInput{
		TenantID: f.tenantID, UserID: userID, AuthVersion: oldVersion,
	}); !errors.Is(err, usersession.ErrAuthenticationStale) {
		t.Fatalf("旧认证结果签发 err=%v，期望 ErrAuthenticationStale", err)
	}

	var newVersion int
	if err := pool.QueryRow(ctx, `
SELECT password_version
FROM users
WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&newVersion); err != nil {
		t.Fatalf("读取新认证版本: %v", err)
	}
	if newVersion != oldVersion+1 {
		t.Fatalf("认证版本=%d，期望 %d；删除版本递增会让旧认证结果重新生效", newVersion, oldVersion+1)
	}
	if _, err := sessions.Create(ctx, usersession.CreateInput{
		TenantID: f.tenantID, UserID: userID, AuthVersion: newVersion,
	}); err != nil {
		t.Fatalf("新认证结果应可签发会话: %v", err)
	}
}

func TestAdminSecurityRecoveryRejectsStaleFactorAndBindingWrites(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserMutationStore(pool)
	userID := f.seedUser("tx-stale-security-write", "active", "user", "0.00000000")
	familyID := seedAdminMutationSession(t, ctx, f, userID, "stale-security-write")
	var authVersion int
	if err := pool.QueryRow(ctx, `
SELECT password_version
FROM users
WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&authVersion); err != nil {
		t.Fatalf("读取认证版本: %v", err)
	}
	audit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: admin.RoleTenantOperator}
	if _, _, err := store.ResetPasskeysWithAudit(ctx, f.tenantID, userID, audit); err != nil {
		t.Fatalf("管理员安全恢复: %v", err)
	}

	authService := userauth.NewService(userauth.NewPostgresStore(pool))
	_, err := authService.LinkVerifiedSocialIdentityForSession(
		ctx,
		f.tenantID,
		userID,
		userauth.VerifiedIdentity{
			Provider: userauth.SocialProviderTelegram,
			Subject:  "stale-subject-" + f.suffix,
		},
		familyID.String(),
		authVersion,
	)
	if !errors.Is(err, userauth.ErrAuthenticationStale) {
		t.Fatalf("旧会话绑定社交身份 err=%v，期望 ErrAuthenticationStale", err)
	}

	passkeyStore := passkey.NewPostgresStore(pool)
	_, err = passkeyStore.SaveCredentialForSession(
		ctx,
		passkey.CredentialRecord{
			TenantID: f.tenantID, UserID: userID,
			CredentialID: []byte("stale-credential-" + f.suffix),
			PublicKey:    []byte("stale-public-key"),
		},
		familyID.String(),
		authVersion,
	)
	if !errors.Is(err, passkey.ErrSecurityStateChanged) {
		t.Fatalf("旧会话写 Passkey err=%v，期望 ErrSecurityStateChanged", err)
	}

	twoFAStore := twofa.NewPostgresStore(pool)
	err = twoFAStore.RunActiveSessionMutation(
		ctx,
		f.tenantID,
		userID,
		familyID.String(),
		authVersion,
		func(twofa.Store) error {
			t.Fatal("旧会话不应进入 2FA 写事务")
			return nil
		},
	)
	if !errors.Is(err, twofa.ErrAuthenticationStale) {
		t.Fatalf("旧会话写 2FA err=%v，期望 ErrAuthenticationStale", err)
	}

	assertRowCount(t, ctx, f, `
SELECT count(*)
FROM social_identity_links
WHERE tenant_id=$1 AND user_id=$2`, userID, 0)
	assertRowCount(t, ctx, f, `
SELECT count(*)
FROM passkey_credentials
WHERE tenant_id=$1 AND user_id=$2`, userID, 0)
}

func TestAdminUserStatusCannotBypassDedicatedRecoveryState(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserMutationStore(pool)
	userID := f.seedUser("tx-reset-required", "reset_required", "user", "0.00000000")
	keyID := seedAdminMutationAPIKey(t, ctx, f, userID)
	session := seedAdminMutationSession(t, ctx, f, userID, "reset-required")
	audit := unlockAuditInput{ActorID: "admin_token:305", ActorRole: admin.RoleTenantOperator}

	if _, err := store.SetUserStatusWithAudit(ctx, f.tenantID, userID, "active", "", audit); !errors.Is(err, errUserStatusTransitionConflict) {
		t.Fatalf("reset_required -> active err=%v want status transition conflict", err)
	}
	assertAdminUserMutationState(t, ctx, f, userID, "reset_required", "default", "", false)
	assertAdminMutationSession(t, ctx, f, session, false)
	var keyStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("读取 API Key 状态: %v", err)
	}
	if keyStatus != "active" {
		t.Fatalf("被拒状态转换仍修改 API Key: %q", keyStatus)
	}
}

func seedAdminMutationSession(
	t *testing.T,
	ctx context.Context,
	f *adminUsersFixture,
	userID int64,
	seed string,
) uuid.UUID {
	t.Helper()
	familyID := uuid.New()
	if _, err := f.pool.Exec(ctx, `
INSERT INTO session_families (id, tenant_id, user_id, status, generation)
VALUES ($1, $2, $3, 'active', 1)`, familyID, f.tenantID, userID); err != nil {
		t.Fatalf("插入会话族: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at)
VALUES ($1, $2, $3, $4, 1, 'active', NOW() + INTERVAL '1 day')`,
		uuid.New(), f.tenantID, familyID, []byte("admin-refresh-"+seed+"-"+f.suffix)); err != nil {
		t.Fatalf("插入刷新令牌: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at)
VALUES ($1, $2, $3, $4, 1, NOW() + INTERVAL '1 hour')`,
		uuid.New(), f.tenantID, familyID, []byte("admin-session-"+seed+"-"+f.suffix)); err != nil {
		t.Fatalf("插入会话令牌: %v", err)
	}
	return familyID
}

func seedAdminMutationAPIKey(
	t *testing.T,
	ctx context.Context,
	f *adminUsersFixture,
	userID int64,
) int64 {
	t.Helper()
	var keyID int64
	if err := f.pool.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id,user_id,name,key_hash,key_prefix,status)
VALUES($1,$2,'status-conflict',$3,$4,'active')
RETURNING id`,
		f.tenantID,
		userID,
		"status-conflict-hash-"+f.suffix,
		"hk_status_"+f.suffix[:8],
	).Scan(&keyID); err != nil {
		t.Fatalf("插入 API Key: %v", err)
	}
	return keyID
}

func assertAdminMutationSession(
	t *testing.T,
	ctx context.Context,
	f *adminUsersFixture,
	familyID uuid.UUID,
	wantRevoked bool,
) {
	t.Helper()
	var familyRevoked, refreshRevoked, sessionRevoked bool
	if err := f.pool.QueryRow(ctx,
		`SELECT status='revoked' FROM session_families WHERE id=$1`,
		familyID).Scan(&familyRevoked); err != nil {
		t.Fatalf("读取会话族状态: %v", err)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT status='revoked' FROM refresh_tokens WHERE family_id=$1`,
		familyID).Scan(&refreshRevoked); err != nil {
		t.Fatalf("读取刷新令牌状态: %v", err)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL FROM session_tokens WHERE family_id=$1`,
		familyID).Scan(&sessionRevoked); err != nil {
		t.Fatalf("读取会话令牌状态: %v", err)
	}
	if familyRevoked != wantRevoked || refreshRevoked != wantRevoked || sessionRevoked != wantRevoked {
		t.Fatalf(
			"会话状态=(family:%v refresh:%v session:%v)，期望全部 %v",
			familyRevoked, refreshRevoked, sessionRevoked, wantRevoked,
		)
	}
}

func assertMutationFails(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("日志写入失败时业务改动必须回滚")
	}
}

func assertAdminUserMutationState(
	t *testing.T,
	ctx context.Context,
	f *adminUsersFixture,
	userID int64,
	wantStatus, wantGroup, wantRemark string,
	wantDeleted bool,
) {
	t.Helper()
	var status, group, remark string
	var deleted bool
	if err := f.pool.QueryRow(ctx, `
SELECT status, user_group, remark, deleted_at IS NOT NULL
FROM users
WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, userID,
	).Scan(&status, &group, &remark, &deleted); err != nil {
		t.Fatalf("读取用户状态: %v", err)
	}
	if status != wantStatus || group != wantGroup || remark != wantRemark || deleted != wantDeleted {
		t.Fatalf("用户状态=(%q,%q,%q,%v)，期望=(%q,%q,%q,%v)",
			status, group, remark, deleted, wantStatus, wantGroup, wantRemark, wantDeleted)
	}
}

func assertTwoFAEnabled(t *testing.T, ctx context.Context, f *adminUsersFixture, userID int64, want bool) {
	t.Helper()
	var enabled bool
	if err := f.pool.QueryRow(ctx, `
SELECT is_enabled FROM two_factor_settings WHERE tenant_id=$1 AND user_id=$2`,
		f.tenantID, userID,
	).Scan(&enabled); err != nil {
		t.Fatalf("读取 2FA 状态: %v", err)
	}
	if enabled != want {
		t.Fatalf("2FA enabled=%v，期望 %v", enabled, want)
	}
}

func assertRowCount(
	t *testing.T,
	ctx context.Context,
	f *adminUsersFixture,
	query string,
	userID int64,
	want int,
) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(ctx, query, f.tenantID, userID).Scan(&count); err != nil {
		t.Fatalf("读取关联行数: %v", err)
	}
	if count != want {
		t.Fatalf("关联行数=%d，期望 %d", count, want)
	}
}
