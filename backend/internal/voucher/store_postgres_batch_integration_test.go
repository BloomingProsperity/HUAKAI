//go:build integration_pg

package voucher

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openVoucherPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestGetBatchReturnsAllVouchersBeyond1000NewerWindow guards GetBatch must return the
// COMPLETE voucher set of the requested batch, even when the tenant has >1000 newer vouchers in
// other batches. The old implementation reused a tenant-wide list capped at LIMIT 1000 ordered by
// id DESC and filtered in memory, so an older batch fell entirely outside the newest-1000 window
// and was silently returned empty (still HTTP 200) — corrupting admin export / fraud review /
// campaign reconciliation.
//
// Mutation check: revert GetBatch to `ListVouchers(ListInput{TenantID, Limit:1000})` + in-memory
// batch filter; the 2 target-batch vouchers are pushed out by the 1000 newer ones → GetBatch
// returns 0 vouchers → both assertions go red. Discriminating fixture: exactly 1000 newer rows
// (the documented max batch size) so the older batch sits just past the cap.
func TestGetBatchReturnsAllVouchersBeyond1000NewerWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openVoucherPool(t, ctx)

	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"voucher-s2091-"+time.Now().Format("150405.000000000")).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM voucher_batch WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	vf := time.Now().Add(-time.Hour)
	vu := time.Now().Add(24 * time.Hour)
	newBatch := func(reqCount int) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
INSERT INTO voucher_batch (tenant_id, created_by_admin_id, requested_count, created_count, amount_cents, valid_from, valid_until)
VALUES ($1,1,$2,$2,$3,$4,$5) RETURNING id`, tenantID, reqCount, 500, vf, vu).Scan(&id); err != nil {
			t.Fatalf("insert batch: %v", err)
		}
		return id
	}

	// 目标(较旧)批次 B1:2 张 voucher,先插入 → id 较小。
	b1 := newBatch(2)
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `
INSERT INTO voucher (tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, valid_from, valid_until, revoked_reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,'')`,
			tenantID, b1,
			[]byte(fmt.Sprintf("b1-h-%d-%d", tenantID, i)), fmt.Sprintf("b1-fp-%d-%d", tenantID, i),
			500, vf, vu); err != nil {
			t.Fatalf("insert b1 voucher %d: %v", i, err)
		}
	}

	// 较新批次 B2:1000 张 voucher(id 全部大于 B1),把 B1 挤出租户级最新 1000 窗口。
	b2 := newBatch(1000)
	prefix := fmt.Sprintf("b2-%d", tenantID) // tenant-unique 前缀,避免触碰可能存在的全局唯一约束
	if _, err := pool.Exec(ctx, `
INSERT INTO voucher (tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, valid_from, valid_until, revoked_reason)
SELECT $1, $2, convert_to($5||'-h-'||g, 'UTF8'), $5||'-fp-'||g, 500, $3, $4, ''
FROM generate_series(1, 1000) AS g`, tenantID, b2, vf, vu, prefix); err != nil {
		t.Fatalf("bulk insert b2 vouchers: %v", err)
	}

	store := NewPostgresStore(pool)
	res, err := store.GetBatch(ctx, tenantID, b1)
	if err != nil {
		t.Fatalf("GetBatch(b1): %v", err)
	}
	if len(res.Vouchers) != 2 {
		t.Fatalf("GetBatch must return all 2 vouchers of the older batch even behind 1000 newer ones; got %d", len(res.Vouchers))
	}
	for _, v := range res.Vouchers {
		if v.BatchID == nil || *v.BatchID != b1 {
			t.Fatalf("GetBatch returned a voucher not belonging to batch %d: batch_id=%v", b1, v.BatchID)
		}
	}
}
