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

// TestPGCredentialUsageCASRejectsRegression 守护 sign_count CAS:UpdateCredentialUsage
// 只允许严格递增(或双 0)的写入,挡并发竞态下绕过克隆检测的非递增盲写。
// 变异证伪:摘掉 UPDATE 的 `AND (sign_count < $3 OR (sign_count=0 AND $3=0))`,则
// asserted=3 对 stored=5 会盲写成功(sign_count 被改成 3)→ 本测试转红。
// 另一变异:摘掉 ErrNoRows 后的 credentialExists 区分,则不存在凭据也返回
// ErrCloneDetected → 末段 NotFound 断言转红。
func TestPGCredentialUsageCASRejectsRegression(t *testing.T) {
	ctx := context.Background()
	store, pool := passkeyPGStore(t, ctx)
	f := seedPasskeyFixture(t, ctx, pool)
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	credID := []byte("cred-" + uuid.NewString())
	if _, err := store.SaveCredential(ctx, CredentialRecord{
		TenantID: f.tenantID, UserID: f.userA, CredentialID: credID, PublicKey: []byte("pk-cas"),
		SignCount: 5, AttestationType: "none", Transports: []string{"internal"}, Name: "k", CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	// 回退(asserted=3 < stored=5):CAS 失败,凭据存在 → ErrCloneDetected。
	if _, err := store.UpdateCredentialUsage(ctx, f.tenantID, credID, 3, false, now.Add(time.Hour)); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("回退计数 err=%v want ErrCloneDetected", err)
	}
	// 关键不变量:盲写没发生,库内 sign_count 仍是 5。
	if got, err := store.GetCredentialByCredentialID(ctx, f.tenantID, credID); err != nil || got.SignCount != 5 {
		t.Fatalf("CAS 失败后 sign_count 应仍为 5,got=%d err=%v", got.SignCount, err)
	}
	// 重放(asserted=5 == stored=5,非双 0):同样 CAS 失败 → ErrCloneDetected。
	if _, err := store.UpdateCredentialUsage(ctx, f.tenantID, credID, 5, false, now.Add(time.Hour)); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("等值重放 err=%v want ErrCloneDetected", err)
	}
	// 正常递增(asserted=10 > 5):成功。
	if updated, err := store.UpdateCredentialUsage(ctx, f.tenantID, credID, 10, false, now.Add(2*time.Hour)); err != nil || updated.SignCount != 10 {
		t.Fatalf("递增写应成功,got=%d err=%v", updated.SignCount, err)
	}
	// 不存在的凭据:必须区分成 NotFound,不能误报 CloneDetected。
	if _, err := store.UpdateCredentialUsage(ctx, f.tenantID, []byte("nope-"+uuid.NewString()), 1, false, now); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("不存在凭据 err=%v want ErrCredentialNotFound", err)
	}
	// 0 计数设备(不支持计数):stored=0 + asserted=0 允许写。
	zeroID := []byte("cred0-" + uuid.NewString())
	if _, err := store.SaveCredential(ctx, CredentialRecord{
		TenantID: f.tenantID, UserID: f.userB, CredentialID: zeroID, PublicKey: []byte("pk0"),
		SignCount: 0, AttestationType: "none", Transports: []string{"internal"}, Name: "k0", CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCredential 0计数: %v", err)
	}
	if _, err := store.UpdateCredentialUsage(ctx, f.tenantID, zeroID, 0, false, now.Add(time.Hour)); err != nil {
		t.Fatalf("0计数设备 0→0 应允许,err=%v", err)
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
