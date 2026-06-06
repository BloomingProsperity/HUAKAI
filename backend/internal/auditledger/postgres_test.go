//go:build integration_pg
// +build integration_pg

// 跑法：
//   HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
//     go test -tags=integration_pg ./internal/auditledger/...
//
// 不带 HUAKAI_DATABASE_URL 的话整组测试被 build tag 跳过；本文件不会进入
// 默认 `go test ./...` 的执行集合。

package auditledger

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping PG integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// migrations 0013 必须已 apply；只检表存在不主动建表。
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT 1 FROM information_schema.tables WHERE table_name='audit_ledger_entries' LIMIT 1",
	).Scan(&n); err != nil {
		pool.Close()
		t.Skipf("audit_ledger_entries 表未建（migration 0013 未 apply）：%v", err)
	}
	return pool
}

func truncateLedger(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "TRUNCATE audit_ledger_entries RESTART IDENTITY"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func seedLedgerTenant(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		name,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM audit_ledger_entries WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID
}

func TestPostgresLedger_AppendAndGet(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, err := NewPostgresLedger(pool, signer)
	if err != nil {
		t.Fatalf("NewPostgresLedger: %v", err)
	}
	ctx := context.Background()

	entry := LedgerEntry{
		LedgerID:  "lid_pg_1",
		RequestID: "req_pg_1",
		HopChain: []proto.HopAttestation{
			{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"},
		},
	}
	out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, entry))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if out.PrevMerkleRoot != ZeroRoot {
		t.Error("first entry PrevMerkleRoot must be ZeroRoot")
	}
	if out.MerkleRoot == ZeroRoot {
		t.Error("MerkleRoot must be non-zero")
	}
	if out.PubkeyFingerprint != signer.Fingerprint() {
		t.Errorf("fp mismatch")
	}
	sigBytes, _ := base64.StdEncoding.DecodeString(out.Signature)
	if len(sigBytes) != 64 {
		t.Errorf("sig len wrong: %d", len(sigBytes))
	}

	got, err := l.GetByRequestID(ctx, "req_pg_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LedgerID == "" || got.LedgerID != out.LedgerID {
		t.Errorf("LedgerID mismatch: got %q want %q", got.LedgerID, out.LedgerID)
	}
	if got.PubkeyFingerprint != out.PubkeyFingerprint {
		t.Error("fp roundtrip mismatch")
	}
}

func TestPostgresLedger_GetNotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	_, err := l.GetByRequestID(context.Background(), "missing_req")
	if !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Errorf("expected ErrLedgerEntryNotFound, got %v", err)
	}
}

func TestAT_SECURITY_W1_B14_PostgresLedgerTenantScopedLookup(t *testing.T) {
	// Risk killed: the database lookup must constrain request_id by tenant scope
	// so a known request_id cannot disclose another tenant's ledger row.
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	suffix := uuid.NewString()
	tenantA := seedLedgerTenant(t, pool, "ledger-scope-a-"+suffix)
	tenantB := seedLedgerTenant(t, pool, "ledger-scope-b-"+suffix)
	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	entryA, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_scope_a_" + suffix, TenantID: tenantA}))
	if err != nil {
		t.Fatalf("append tenant A: %v", err)
	}
	if _, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_scope_b_" + suffix, TenantID: tenantB})); err != nil {
		t.Fatalf("append tenant B: %v", err)
	}
	got, err := l.GetByRequestIDAndTenantScope(ctx, entryA.RequestID, TenantScopeRef(tenantA))
	if err != nil {
		t.Fatalf("tenant A scoped lookup: %v", err)
	}
	if got.LedgerID != entryA.LedgerID || got.TenantID != tenantA {
		t.Fatalf("got wrong row: %+v want ledger=%s tenant=%d", got, entryA.LedgerID, tenantA)
	}
	if _, err := pool.Exec(ctx, `UPDATE tenants SET deleted_at=NOW() WHERE id=$1`, tenantA); err != nil {
		t.Fatalf("soft delete tenant A: %v", err)
	}
	gotAfterDelete, err := l.GetByRequestIDAndTenantScope(ctx, entryA.RequestID, TenantScopeRef(tenantA))
	if err != nil {
		t.Fatalf("soft-deleted tenant historical scoped lookup: %v", err)
	}
	if gotAfterDelete.LedgerID != entryA.LedgerID || gotAfterDelete.TenantID != tenantA {
		t.Fatalf("soft-deleted tenant lookup got wrong row: %+v want ledger=%s tenant=%d", gotAfterDelete, entryA.LedgerID, tenantA)
	}
	if _, err := l.GetByRequestIDAndTenantScope(ctx, entryA.RequestID, TenantScopeRef(tenantB)); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Fatalf("tenant B scope must not read tenant A row, got %v", err)
	}
	if _, err := l.GetByRequestIDAndTenantScope(ctx, entryA.RequestID, ""); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Fatalf("empty tenant scope must not read tenant A row, got %v", err)
	}
}

func TestPostgresLedger_ListByRangeTenantScopedAndBounded(t *testing.T) {
	// Mutation: remove the SQL tenant_id predicate after resolving tenant scope;
	// this test fails because tenant B's in-range request leaks into tenant A.
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	suffix := uuid.NewString()
	tenantA := seedLedgerTenant(t, pool, "ledger-range-a-"+suffix)
	tenantB := seedLedgerTenant(t, pool, "ledger-range-b-"+suffix)
	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_before_" + suffix, TenantID: tenantA, Timestamp: "2026-06-01T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_a_1_" + suffix, TenantID: tenantA, Timestamp: "2026-06-02T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_a_2_" + suffix, TenantID: tenantA, Timestamp: "2026-06-03T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_b_" + suffix, TenantID: tenantB, Timestamp: "2026-06-02T12:00:00Z"}))

	from := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 3, 23, 59, 59, 0, time.UTC)
	rows, err := l.ListByRange(ctx, TenantScopeRef(tenantA), from, to, 10)
	if err != nil {
		t.Fatalf("ListByRange: %v", err)
	}
	if got, want := ledgerRequestIDs(rows), []string{"req_pg_a_1_" + suffix, "req_pg_a_2_" + suffix}; !equalStrings(got, want) {
		t.Fatalf("range request ids=%v want %v", got, want)
	}

	limited, err := l.ListByRange(ctx, TenantScopeRef(tenantA), from, to, 1)
	if err != nil {
		t.Fatalf("ListByRange limited: %v", err)
	}
	if got, want := ledgerRequestIDs(limited), []string{"req_pg_a_1_" + suffix}; !equalStrings(got, want) {
		t.Fatalf("limited range request ids=%v want %v", got, want)
	}
}

func TestPostgresLedger_ListByRequestIDsTenantScoped(t *testing.T) {
	// Mutation: remove tenant_id filtering from ListByRequestIDs; this test fails
	// because a requested tenant B row leaks into tenant A's audit bundle.
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	suffix := uuid.NewString()
	tenantA := seedLedgerTenant(t, pool, "ledger-ids-a-"+suffix)
	tenantB := seedLedgerTenant(t, pool, "ledger-ids-b-"+suffix)
	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_id_a_1_" + suffix, TenantID: tenantA}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_id_a_2_" + suffix, TenantID: tenantA}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_pg_id_b_" + suffix, TenantID: tenantB}))

	rows, err := l.ListByRequestIDs(ctx, TenantScopeRef(tenantA), []string{"req_pg_id_b_" + suffix, "req_pg_id_a_2_" + suffix, "missing", "req_pg_id_a_1_" + suffix}, 10)
	if err != nil {
		t.Fatalf("ListByRequestIDs: %v", err)
	}
	if got, want := ledgerRequestIDs(rows), []string{"req_pg_id_a_1_" + suffix, "req_pg_id_a_2_" + suffix}; !equalStrings(got, want) {
		t.Fatalf("request id rows=%v want %v", got, want)
	}
}

func TestPostgresLedger_ChainContinuity(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	var prevRoot [32]byte
	for i := 0; i < 100; i++ {
		entry := LedgerEntry{
			RequestID: fmt.Sprintf("req_pg_%d", i),
		}
		out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, entry))
		if err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
		if out.PrevMerkleRoot != prevRoot {
			t.Errorf("entry %d: prev_root mismatch", i)
		}
		prevRoot = out.MerkleRoot
	}

	latest, err := l.LatestMerkleRoot(ctx)
	if err != nil {
		t.Fatalf("LatestMerkleRoot: %v", err)
	}
	if latest != prevRoot {
		t.Errorf("latest root != last appended root")
	}

	if size := l.Size(ctx); size != 100 {
		t.Errorf("Size: got %d want 100", size)
	}
}

func TestPostgresLedger_RejectMissingFields(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	if _, err := l.Append(ctx, PreparedEntry{}); err == nil {
		t.Error("missing RequestID must reject")
	}
	if out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "x"})); err != nil || out.LedgerID == "" {
		t.Errorf("missing LedgerID must auto-generate, out=%+v err=%v", out, err)
	}
}

func TestPostgresLedger_NilSignerRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	if _, err := NewPostgresLedger(pool, nil); !errors.Is(err, ErrSignerNil) {
		t.Errorf("expected ErrSignerNil, got %v", err)
	}
}

func TestPostgresLedger_AppendOnlyTriggerRejectsUpdateDelete(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_append_only"}))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"UPDATE audit_ledger_entries SET signature = signature WHERE request_id = $1",
		out.RequestID,
	); err == nil {
		t.Fatal("UPDATE must be rejected by append-only trigger")
	}
	if _, err := pool.Exec(ctx,
		"DELETE FROM audit_ledger_entries WHERE request_id = $1",
		out.RequestID,
	); err == nil {
		t.Fatal("DELETE must be rejected by append-only trigger")
	}
}

func TestPostgresLedger_NilPoolRejected(t *testing.T) {
	signer, _ := sign.GenerateKey()
	if _, err := NewPostgresLedger(nil, signer); err == nil {
		t.Error("nil pool must reject")
	}
}

func TestPostgresLedger_UniqueRequestIDConstraint(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	entry := LedgerEntry{LedgerID: "lid_a", RequestID: "req_dup"}
	if _, err := l.Append(ctx, mustPrepareForAppend(t, ctx, entry)); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	// 第二次同 request_id 应 fail（PG UNIQUE 约束触发）。
	entry2 := LedgerEntry{LedgerID: "lid_b", RequestID: "req_dup"}
	if _, err := l.Append(ctx, mustPrepareForAppend(t, ctx, entry2)); err == nil {
		t.Error("duplicate request_id must fail UNIQUE constraint")
	}
}
