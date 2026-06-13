//go:build integration_pg

package adminuserhttp

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// TestCreateDeleteUser_RealStoreAndMigrationConstraint exercises the real
// CreateUser / SoftDeleteForTenant SQL AND proves migration 0138 admits the new
// admin_audit_events.action values 'create_user' / 'delete_user' (a stubbed
// audit store would hide a missing CHECK-constraint value). Also confirms the
// soft-delete leaves the email reusable via the partial unique index.
func TestCreateDeleteUser_RealStoreAndMigrationConstraint(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)

	createDeps := Deps{
		Auth:        usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store:       admindb.New(pool),
		UserCreator: NewPostgresUserCreateStore(pool),
		Audit:       admindb.New(pool),
	}
	email := "s4-" + f.suffix + "@x.test"
	rec := invokeAdminUsersBody(t, createDeps, http.MethodPost, "/admin/v1/users",
		`{"email":"`+email+`","password":"longenough1"}`)
	assertStatus(t, rec, http.StatusCreated)
	var created struct {
		ID   int64  `json:"id"`
		Role string `json:"role"`
	}
	decodeBody(t, rec, &created)
	if created.ID == 0 || created.Role != "user" {
		t.Fatalf("created=%+v want id>0 role=user", created)
	}

	var status, hash string
	var verified bool
	if err := pool.QueryRow(ctx,
		`SELECT status, email_verified, password_hash FROM users WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, created.ID).Scan(&status, &verified, &hash); err != nil {
		t.Fatalf("read created user: %v", err)
	}
	if status != "active" || !verified || hash == "longenough1" {
		t.Fatalf("created user state status=%q verified=%v plaintextHash=%v", status, verified, hash == "longenough1")
	}
	assertLatestAuditAction(t, ctx, pool, f.tenantID, created.ID, "create_user")

	delDeps := Deps{
		Auth:            usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store:           admindb.New(pool),
		UserSoftDeleter: NewPostgresUserSoftDeleteStore(pool),
		Audit:           admindb.New(pool),
	}
	delRec := invokeAdminUsersBody(t, delDeps, http.MethodDelete,
		"/admin/v1/users/"+strconv.FormatInt(created.ID, 10), "")
	assertStatus(t, delRec, http.StatusOK)

	var delStatus string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, deleted_at FROM users WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, created.ID).Scan(&delStatus, &deletedAt); err != nil {
		t.Fatalf("read deleted user: %v", err)
	}
	if delStatus != "deleted" || deletedAt == nil {
		t.Fatalf("soft-delete state status=%q deleted_at=%v want deleted/non-nil", delStatus, deletedAt)
	}
	assertLatestAuditAction(t, ctx, pool, f.tenantID, created.ID, "delete_user")

	// Soft-delete frees the email (partial unique index WHERE deleted_at IS NULL).
	reRec := invokeAdminUsersBody(t, createDeps, http.MethodPost, "/admin/v1/users",
		`{"email":"`+email+`","password":"longenough1"}`)
	assertStatus(t, reRec, http.StatusCreated)
}

func assertLatestAuditAction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, targetID int64, want string) {
	t.Helper()
	var action, targetType string
	if err := pool.QueryRow(ctx,
		`SELECT action, target_type FROM admin_audit_events
		 WHERE tenant_id=$1 AND target_id=$2 AND action=$3
		 ORDER BY id DESC LIMIT 1`,
		tenantID, targetID, want).Scan(&action, &targetType); err != nil {
		t.Fatalf("read %s audit (migration 0138 CHECK must admit it): %v", want, err)
	}
	if action != want || targetType != "user" {
		t.Fatalf("audit action=%q target=%q want %s/user", action, targetType, want)
	}
}
