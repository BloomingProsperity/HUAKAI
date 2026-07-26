//go:build integration_pg

package twofa

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGTwoFAStateAndSessionInvalidationCommitTogether(t *testing.T) {
	ctx := context.Background()
	pool := openTwoFASecurityPool(t, ctx)
	t.Cleanup(pool.Close)
	tenantID, userID := seedTwoFASecurityUser(t, ctx, pool)
	t.Cleanup(func() { cleanupTwoFASecurityUser(t, ctx, pool, tenantID) })

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewPostgresStore(pool),
		mustKeyProvider(t),
		WithNow(func() time.Time { return now }),
	)
	setup, err := service.Setup(ctx, SetupInput{
		TenantID: tenantID, UserID: userID, AccountName: "twofa-security@example.test",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	current := seedTwoFASecuritySession(t, ctx, pool, tenantID, userID, "enable-current")
	other := seedTwoFASecuritySession(t, ctx, pool, tenantID, userID, "enable-other")

	status, err := service.EnableWithSessionInvalidation(ctx, VerifyInput{
		TenantID: tenantID,
		UserID:   userID,
		Code:     codeFromSetupSecret(t, setup.Secret, now),
	}, current.String(), nil)
	if err != nil {
		t.Fatalf("EnableWithSessionInvalidation: %v", err)
	}
	if !status.Enabled || status.SessionsRevoked != 1 {
		t.Fatalf("enable status=%+v want enabled and one revoked family", status)
	}
	assertTwoFASecurityEnabled(t, ctx, pool, tenantID, userID, true)
	assertTwoFASecurityFamily(t, ctx, pool, current, "active")
	assertTwoFASecurityFamily(t, ctx, pool, other, "revoked")

	afterEnable := seedTwoFASecuritySession(t, ctx, pool, tenantID, userID, "disable-other")
	revoked, err := service.DisableWithSessionInvalidation(ctx, VerifyInput{
		TenantID: tenantID,
		UserID:   userID,
		Code:     codeFromSetupSecret(t, setup.Secret, now),
	}, current.String(), nil)
	if err != nil {
		t.Fatalf("DisableWithSessionInvalidation: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("disable revoked families=%d want 1", revoked)
	}
	assertTwoFASecurityEnabled(t, ctx, pool, tenantID, userID, false)
	assertTwoFASecurityFamily(t, ctx, pool, current, "active")
	assertTwoFASecurityFamily(t, ctx, pool, afterEnable, "revoked")
}

// 2FA 行更新发生在其他会话的事务内撤销之后。故意让该更新失败，证明前面的会话变化
// 也会回滚；若两者仍是两个事务，other 会错误地保持 revoked。
func TestPGTwoFAStateFailureRollsBackSessionInvalidation(t *testing.T) {
	ctx := context.Background()
	pool := openTwoFASecurityPool(t, ctx)
	t.Cleanup(pool.Close)
	tenantID, userID := seedTwoFASecurityUser(t, ctx, pool)
	t.Cleanup(func() { cleanupTwoFASecurityUser(t, ctx, pool, tenantID) })

	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	service := NewService(
		NewPostgresStore(pool),
		mustKeyProvider(t),
		WithNow(func() time.Time { return now }),
	)
	setup, err := service.Setup(ctx, SetupInput{
		TenantID: tenantID, UserID: userID, AccountName: "twofa-rollback@example.test",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	current := seedTwoFASecuritySession(t, ctx, pool, tenantID, userID, "rollback-current")
	other := seedTwoFASecuritySession(t, ctx, pool, tenantID, userID, "rollback-other")
	installTwoFAStateFailure(t, ctx, pool)

	_, err = service.EnableWithSessionInvalidation(ctx, VerifyInput{
		TenantID: tenantID,
		UserID:   userID,
		Code:     codeFromSetupSecret(t, setup.Secret, now),
	}, current.String(), nil)
	if err == nil {
		t.Fatalf("EnableWithSessionInvalidation err=nil want injected 2FA state failure")
	}
	if errors.Is(err, ErrSessionInvalidation) {
		t.Fatalf("2FA row failure misclassified as session invalidation: %v", err)
	}
	assertTwoFASecurityEnabled(t, ctx, pool, tenantID, userID, false)
	assertTwoFASecurityFamily(t, ctx, pool, current, "active")
	assertTwoFASecurityFamily(t, ctx, pool, other, "active")
}

func openTwoFASecurityPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("HUAKAI_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	return pool
}

func seedTwoFASecurityUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"twofa-security-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO users (tenant_id, email, display_name, status)
VALUES ($1, $2, 'TwoFA Security', 'active')
RETURNING id`, tenantID, "twofa-security-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return tenantID, userID
}

func seedTwoFASecuritySession(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, userID int64,
	seed string,
) uuid.UUID {
	t.Helper()
	familyID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO session_families (id, tenant_id, user_id, status, generation)
VALUES ($1, $2, $3, 'active', 1)`, familyID, tenantID, userID); err != nil {
		t.Fatalf("insert session family: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at)
VALUES ($1, $2, $3, $4, 1, 'active', NOW() + INTERVAL '1 day')`,
		uuid.New(), tenantID, familyID, []byte("twofa-refresh-"+seed+"-"+familyID.String())); err != nil {
		t.Fatalf("insert refresh token: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at)
VALUES ($1, $2, $3, $4, 1, NOW() + INTERVAL '1 hour')`,
		uuid.New(), tenantID, familyID, []byte("twofa-session-"+seed+"-"+familyID.String())); err != nil {
		t.Fatalf("insert session token: %v", err)
	}
	return familyID
}

func assertTwoFASecurityEnabled(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, userID int64,
	want bool,
) {
	t.Helper()
	var got bool
	if err := pool.QueryRow(ctx,
		`SELECT is_enabled FROM two_factor_settings WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&got); err != nil {
		t.Fatalf("read 2FA state: %v", err)
	}
	if got != want {
		t.Fatalf("two-factor enabled=%v want %v", got, want)
	}
}

func assertTwoFASecurityFamily(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	familyID uuid.UUID,
	want string,
) {
	t.Helper()
	var familyStatus, refreshStatus string
	var sessionRevoked bool
	if err := pool.QueryRow(ctx,
		`SELECT status FROM session_families WHERE id=$1`, familyID).Scan(&familyStatus); err != nil {
		t.Fatalf("read family status: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refresh_tokens WHERE family_id=$1`, familyID).Scan(&refreshStatus); err != nil {
		t.Fatalf("read refresh status: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL FROM session_tokens WHERE family_id=$1`, familyID).Scan(&sessionRevoked); err != nil {
		t.Fatalf("read session token status: %v", err)
	}
	wantTokenRevoked := want == "revoked"
	if familyStatus != want || refreshStatus != want || sessionRevoked != wantTokenRevoked {
		t.Fatalf(
			"family=%q refresh=%q session_revoked=%v want %q/%q/%v",
			familyStatus, refreshStatus, sessionRevoked,
			want, want, wantTokenRevoked,
		)
	}
}

func installTwoFAStateFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION huakai_test_fail_twofa_state_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.is_enabled IS DISTINCT FROM OLD.is_enabled THEN
        RAISE EXCEPTION 'injected two-factor state failure';
    END IF;
    RETURN NEW;
END;
$$`); err != nil {
		t.Fatalf("create 2FA failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
CREATE TRIGGER huakai_test_fail_twofa_state_update
BEFORE UPDATE ON two_factor_settings
FOR EACH ROW
EXECUTE FUNCTION huakai_test_fail_twofa_state_update()`); err != nil {
		t.Fatalf("create 2FA failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS huakai_test_fail_twofa_state_update ON two_factor_settings`); err != nil {
			t.Errorf("drop 2FA failure trigger: %v", err)
		}
		if _, err := pool.Exec(ctx, `DROP FUNCTION IF EXISTS huakai_test_fail_twofa_state_update()`); err != nil {
			t.Errorf("drop 2FA failure function: %v", err)
		}
	})
}

func cleanupTwoFASecurityUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM refresh_tokens WHERE tenant_id=$1`,
		`DELETE FROM session_tokens WHERE tenant_id=$1`,
		`DELETE FROM session_families WHERE tenant_id=$1`,
		`DELETE FROM two_factor_backup_codes WHERE tenant_id=$1`,
		`DELETE FROM two_factor_settings WHERE tenant_id=$1`,
		`DELETE FROM users WHERE tenant_id=$1`,
		`DELETE FROM tenants WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, tenantID); err != nil {
			t.Fatalf("cleanup tenant %d: %v", tenantID, err)
		}
	}
}
