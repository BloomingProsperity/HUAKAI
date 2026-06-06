//go:build integration_pg

package passkey

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func passkeyPGStore(t *testing.T, ctx context.Context) (*PostgresStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 10})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewPostgresStore(pool), pool
}

type passkeyPGFixture struct {
	tenantID int64
	userA    int64
	userB    int64
}

func seedPasskeyFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) passkeyPGFixture {
	t.Helper()
	suffix := uuid.NewString()
	var f passkeyPGFixture
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "pk-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1,$2,'active') RETURNING id`, f.tenantID, "pk-a-"+suffix).Scan(&f.userA); err != nil {
		t.Fatalf("seed userA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1,$2,'active') RETURNING id`, f.tenantID, "pk-b-"+suffix).Scan(&f.userB); err != nil {
		t.Fatalf("seed userB: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM webauthn_session WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM passkey_credentials WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
	})
	return f
}

func saveCeremony(t *testing.T, ctx context.Context, store *PostgresStore, s CeremonySession) {
	t.Helper()
	if err := store.SaveCeremonySession(ctx, s); err != nil {
		t.Fatalf("SaveCeremonySession: %v", err)
	}
}

func TestPGCeremonySessionSingleUse(t *testing.T) {
	// Mutation killed: replacing the DELETE...RETURNING in ConsumeCeremonySession
	// with a read-only SELECT lets the second consume succeed -> this test fails.
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	sess := CeremonySession{ID: uuid.NewString(), TenantID: f.tenantID, Purpose: PurposeLogin, SessionData: []byte(`{"challenge":"x"}`), ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now}
	saveCeremony(t, ctx, store, sess)
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, Purpose: PurposeLogin, Now: now}); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, Purpose: PurposeLogin, Now: now}); !errors.Is(err, ErrCeremonyNotFound) {
		t.Fatalf("second consume err=%v want ErrCeremonyNotFound", err)
	}
}

func TestPGCeremonySessionExpired(t *testing.T) {
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	sess := CeremonySession{ID: uuid.NewString(), TenantID: f.tenantID, Purpose: PurposeLogin, SessionData: []byte(`{"challenge":"x"}`), ExpiresAt: now.Add(-1 * time.Minute), CreatedAt: now.Add(-10 * time.Minute)}
	saveCeremony(t, ctx, store, sess)
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, Purpose: PurposeLogin, Now: now}); !errors.Is(err, ErrCeremonyExpired) {
		t.Fatalf("consume expired err=%v want ErrCeremonyExpired", err)
	}
}

func TestPGCeremonySessionPurposeScope(t *testing.T) {
	// A register-purpose session (bound to a user) must not be consumable via the
	// wrong purpose, nor via the login scope (user_id IS NULL).
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	sess := CeremonySession{ID: uuid.NewString(), TenantID: f.tenantID, UserID: f.userA, Purpose: PurposeRegister, SessionData: []byte(`{"challenge":"y"}`), ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now}
	saveCeremony(t, ctx, store, sess)
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, UserID: f.userA, Purpose: PurposeLogin, Now: now}); !errors.Is(err, ErrCeremonyNotFound) {
		t.Fatalf("wrong-purpose consume err=%v want ErrCeremonyNotFound", err)
	}
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, Purpose: PurposeRegister, Now: now}); !errors.Is(err, ErrCeremonyNotFound) {
		t.Fatalf("wrong-scope consume err=%v want ErrCeremonyNotFound", err)
	}
	if _, err := store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{ID: sess.ID, TenantID: f.tenantID, UserID: f.userA, Purpose: PurposeRegister, Now: now}); err != nil {
		t.Fatalf("correct consume: %v", err)
	}
}

func TestPGCredentialRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	credID := []byte("cred-" + uuid.NewString())
	saved, err := store.SaveCredential(ctx, CredentialRecord{
		TenantID: f.tenantID, UserID: f.userA, CredentialID: credID, PublicKey: []byte("pk-1"),
		SignCount: 3, AttestationType: "none", Transports: []string{"internal", "hybrid"}, Name: "MacBook", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	got, err := store.GetCredentialByCredentialID(ctx, f.tenantID, credID)
	if err != nil {
		t.Fatalf("GetCredentialByCredentialID: %v", err)
	}
	if got.ID != saved.ID || !bytes.Equal(got.CredentialID, credID) || !bytes.Equal(got.PublicKey, []byte("pk-1")) || got.SignCount != 3 || got.UserID != f.userA {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Transports) != 2 || got.Transports[0] != "internal" {
		t.Fatalf("transports mismatch: %v", got.Transports)
	}
	if got.LastUsedAt != nil {
		t.Fatalf("last_used_at=%v want nil before use", got.LastUsedAt)
	}
	updated, err := store.UpdateCredentialUsage(ctx, f.tenantID, credID, 9, true, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("UpdateCredentialUsage: %v", err)
	}
	if updated.SignCount != 9 || !updated.CloneWarning || updated.LastUsedAt == nil {
		t.Fatalf("update not reflected: %+v", updated)
	}
	reread, err := store.GetCredentialByCredentialID(ctx, f.tenantID, credID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if reread.SignCount != 9 || !reread.CloneWarning || reread.LastUsedAt == nil {
		t.Fatalf("reread not persisted: %+v", reread)
	}
}

func TestPGCredentialTenantIsolation(t *testing.T) {
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	credID := []byte("cred-" + uuid.NewString())
	if _, err := store.SaveCredential(ctx, CredentialRecord{TenantID: f.tenantID, UserID: f.userA, CredentialID: credID, PublicKey: []byte("pk")}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if _, err := store.GetCredentialByCredentialID(ctx, f.tenantID+999999, credID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("cross-tenant get err=%v want ErrCredentialNotFound", err)
	}
}

func TestPGCrossUserDeleteRejected(t *testing.T) {
	// Mutation killed: dropping user_id from DeleteCredential's WHERE clause lets
	// userA delete userB's credential -> the survival assertion fails.
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	credID := []byte("cred-" + uuid.NewString())
	credB, err := store.SaveCredential(ctx, CredentialRecord{TenantID: f.tenantID, UserID: f.userB, CredentialID: credID, PublicKey: []byte("pk")})
	if err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if err := store.DeleteCredential(ctx, f.tenantID, f.userA, credB.ID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("cross-user delete err=%v want ErrCredentialNotFound", err)
	}
	if _, err := store.GetCredentialByCredentialID(ctx, f.tenantID, credID); err != nil {
		t.Fatalf("credential must survive cross-user delete: %v", err)
	}
	if err := store.DeleteCredential(ctx, f.tenantID, f.userB, credB.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestPGCredentialUniquePerTenant(t *testing.T) {
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	credID := []byte("cred-" + uuid.NewString())
	if _, err := store.SaveCredential(ctx, CredentialRecord{TenantID: f.tenantID, UserID: f.userA, CredentialID: credID, PublicKey: []byte("pk")}); err != nil {
		t.Fatalf("first SaveCredential: %v", err)
	}
	if _, err := store.SaveCredential(ctx, CredentialRecord{TenantID: f.tenantID, UserID: f.userA, CredentialID: credID, PublicKey: []byte("pk2")}); !errors.Is(err, ErrDuplicateCredential) {
		t.Fatalf("duplicate SaveCredential err=%v want ErrDuplicateCredential", err)
	}
}
