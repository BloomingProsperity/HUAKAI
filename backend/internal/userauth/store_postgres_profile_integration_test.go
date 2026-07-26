//go:build integration_pg

package userauth

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGUpdateDisplayNamePersistsAndStaysTenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := seedUserAuthProfileTenant(t, ctx, pool, "profile-a-"+suffix)
	tenantB := seedUserAuthProfileTenant(t, ctx, pool, "profile-b-"+suffix)
	userA := seedUserAuthProfileUser(t, ctx, pool, tenantA, "Alice")
	userB := seedUserAuthProfileUser(t, ctx, pool, tenantB, "Bob")
	t.Cleanup(func() {
		cleanupUserAuthProfileTenant(t, ctx, pool, tenantA)
		cleanupUserAuthProfileTenant(t, ctx, pool, tenantB)
	})

	updated, err := store.UpdateDisplayName(ctx, tenantA, userA, "Alice Updated")
	if err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}
	if updated.DisplayName != "Alice Updated" || updated.TenantID != tenantA || updated.ID != userA {
		t.Fatalf("updated user=%+v, want tenant/user scoped Alice Updated", updated)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantA, userA); got != "Alice Updated" {
		t.Fatalf("persisted display_name=%q want Alice Updated; MUTATION: UPDATE no-op should fail", got)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantB, userB); got != "Bob" {
		t.Fatalf("tenant B display_name=%q want Bob", got)
	}

	if _, err := store.UpdateDisplayName(ctx, tenantB, userA, "Cross Tenant"); err != ErrUserNotFound {
		t.Fatalf("cross-tenant update err=%v want ErrUserNotFound; MUTATION: dropping tenant_id from UPDATE should write user A", err)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantA, userA); got != "Alice Updated" {
		t.Fatalf("cross-tenant attempt changed tenant A display_name=%q", got)
	}
}

func TestPGUnlinkSocialIdentityDeletesBinding(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := NewService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "unlink-social-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "unlink-" + suffix + "@example.test", DisplayName: "Password Backed",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGoogle, "pg-google-"+suffix); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	unlinked, err := svc.UnlinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGoogle)
	if err != nil {
		t.Fatalf("UnlinkSocialIdentity: %v", err)
	}
	if !unlinked {
		t.Fatal("UnlinkSocialIdentity deleted=false, want true")
	}
	if _, err := store.GetUserBySocialIdentity(ctx, tenantID, SocialProviderGoogle, "pg-google-"+suffix); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetUserBySocialIdentity after unlink err=%v want ErrUserNotFound", err)
	}
}

func TestPGUnlinkSocialIdentityRejectsLastLoginMethod(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := NewService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "unlink-lockout-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "social-only-" + suffix + "@example.test", DisplayName: "Social Only",
		EmailVerified: true, SocialLoginProvider: SocialProviderGoogle, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGoogle, "pg-google-only-"+suffix); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	if _, err := svc.UnlinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGoogle); !errors.Is(err, ErrLastLoginMethod) {
		t.Fatalf("UnlinkSocialIdentity err=%v want ErrLastLoginMethod; MUTATION: removing the final-login guard makes this pass and deletes the only login path", err)
	}
	stillLinked, err := store.GetUserBySocialIdentity(ctx, tenantID, SocialProviderGoogle, "pg-google-only-"+suffix)
	if err != nil {
		t.Fatalf("social link disappeared after rejected unlink: %v", err)
	}
	if stillLinked.ID != user.ID {
		t.Fatalf("social identity resolved user=%d want %d", stillLinked.ID, user.ID)
	}
}

// TestPGAuthLookupsRejectInactiveTenant 守住密码、社交和会话复核共用的三条认证查询。
// 变异：任一查询移除 tenants 联表或 active 条件，对应断言会重新读到用户并转红。
func TestPGAuthLookupsRejectInactiveTenant(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "inactive-auth-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "inactive-" + suffix + "@example.test",
		DisplayName: "Inactive Tenant User", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	subject := "inactive-social-" + suffix
	if _, err := store.LinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGoogle, subject); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	if _, err := store.GetUserByID(ctx, tenantID, user.ID); err != nil {
		t.Fatalf("active tenant GetUserByID: %v", err)
	}
	if _, err := store.GetUserByEmail(ctx, tenantID, user.Email); err != nil {
		t.Fatalf("active tenant GetUserByEmail: %v", err)
	}
	if _, err := store.GetUserBySocialIdentity(ctx, tenantID, SocialProviderGoogle, subject); err != nil {
		t.Fatalf("active tenant GetUserBySocialIdentity: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='disabled' WHERE id=$1`, tenantID); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}
	for name, lookup := range map[string]func() error{
		"id": func() error {
			_, err := store.GetUserByID(ctx, tenantID, user.ID)
			return err
		},
		"email": func() error {
			_, err := store.GetUserByEmail(ctx, tenantID, user.Email)
			return err
		},
		"social": func() error {
			_, err := store.GetUserBySocialIdentity(ctx, tenantID, SocialProviderGoogle, subject)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := lookup(); !errors.Is(err, ErrUserNotFound) {
				t.Fatalf("停用租户认证查询 err=%v, want ErrUserNotFound", err)
			}
		})
	}

	if _, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "blocked-create-" + suffix + "@example.test",
		Status: UserStatusActive,
	}); !errors.Is(err, ErrRegistrationDisabled) {
		t.Fatalf("停用租户 CreateUser err=%v, want ErrRegistrationDisabled", err)
	}
}

func openUserAuthProfilePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres: %v", err)
	}
	return pool
}

func seedUserAuthProfileTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func seedUserAuthProfileUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, displayName string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1, $2, 'active') RETURNING id`, tenantID, displayName).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func readUserAuthProfileDisplayName(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) string {
	t.Helper()
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&displayName); err != nil {
		t.Fatalf("read display_name: %v", err)
	}
	return displayName
}

func cleanupUserAuthProfileTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM social_identity_links WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup social_identity_links: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup users: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup tenant: %v", err)
	}
}
