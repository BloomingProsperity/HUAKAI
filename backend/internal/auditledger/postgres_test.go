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
	out, err := l.Append(ctx, entry)
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
	if got.LedgerID != "lid_pg_1" {
		t.Errorf("LedgerID mismatch: %q", got.LedgerID)
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

func TestPostgresLedger_ChainContinuity(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	truncateLedger(t, pool)

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	var prevRoot [32]byte
	for i := 0; i < 5; i++ {
		entry := LedgerEntry{
			LedgerID:  fmt.Sprintf("lid_pg_%d", i),
			RequestID: fmt.Sprintf("req_pg_%d", i),
		}
		out, err := l.Append(ctx, entry)
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

	if size := l.Size(ctx); size != 5 {
		t.Errorf("Size: got %d want 5", size)
	}
}

func TestPostgresLedger_RejectMissingFields(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	signer, _ := sign.GenerateKey()
	l, _ := NewPostgresLedger(pool, signer)
	ctx := context.Background()

	if _, err := l.Append(ctx, LedgerEntry{LedgerID: "x"}); err == nil {
		t.Error("missing RequestID must reject")
	}
	if _, err := l.Append(ctx, LedgerEntry{RequestID: "x"}); err == nil {
		t.Error("missing LedgerID must reject")
	}
}

func TestPostgresLedger_NilSignerRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	if _, err := NewPostgresLedger(pool, nil); !errors.Is(err, ErrSignerNil) {
		t.Errorf("expected ErrSignerNil, got %v", err)
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
	if _, err := l.Append(ctx, entry); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	// 第二次同 request_id 应 fail（PG UNIQUE 约束触发）。
	entry2 := LedgerEntry{LedgerID: "lid_b", RequestID: "req_dup"}
	if _, err := l.Append(ctx, entry2); err == nil {
		t.Error("duplicate request_id must fail UNIQUE constraint")
	}
}
