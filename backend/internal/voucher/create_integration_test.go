// HUAKAI · iKun
//go:build integration_pg

package voucher

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func countVouchers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM voucher WHERE tenant_id=$1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count vouchers: %v", err)
	}
	return n
}

// TestPG_CreateVoucher_StatusFromValidity 单券创建在真 PG 成功 + status 由 valid_until 相对 now 的 CASE 决定。
// F-PRE-2 回归守卫 — 判别: 去掉 INSERT CASE 的 ::timestamptz 转型 → pgx 参数 $8 类型推断失败 → 建券崩 → 红。
func TestPG_CreateVoucher_StatusFromValidity(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vcreate-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM voucher_batch WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	store := NewPostgresStore(pool)
	now := time.Now().UTC()

	// 未来到期 → active。
	h1, fp1 := CodeHash(tenantID, NormalizeCode("ACT-"+suffix))
	active, err := store.CreateVoucher(ctx, createVoucherRecord{
		TenantID: tenantID, AdminID: 1, CodeHash: h1, CodeFingerprint: fp1,
		AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	})
	if err != nil {
		t.Fatalf("create active voucher on PG: %v", err)
	}
	if active.Status != StatusActive {
		t.Fatalf("status = %q, want active (valid_until in future)", active.Status)
	}

	// 已过期 (valid_until <= now) → expired (CASE 另一分支, 仍验真 PG 不崩)。
	h2, fp2 := CodeHash(tenantID, NormalizeCode("EXP-"+suffix))
	expired, err := store.CreateVoucher(ctx, createVoucherRecord{
		TenantID: tenantID, AdminID: 1, CodeHash: h2, CodeFingerprint: fp2,
		AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-48 * time.Hour), ValidUntil: now.Add(-time.Hour),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	})
	if err != nil {
		t.Fatalf("create expired voucher on PG: %v", err)
	}
	if expired.Status != StatusExpired {
		t.Fatalf("status = %q, want expired (valid_until past)", expired.Status)
	}
	// 读回校验落库。
	if n := countVouchers(t, ctx, pool, tenantID); n != 2 {
		t.Fatalf("vouchers persisted = %d, want 2", n)
	}
}

// TestPG_CreateBatch_OnPG 批量创建走 insertVoucherTx (另一处 CASE 修复点), 真 PG 不崩 + 批与券落库。
func TestPG_CreateBatch_OnPG(t *testing.T) {
	ctx := context.Background()
	pool := openSubPool(t, ctx)
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vbatch-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM voucher_batch WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	store := NewPostgresStore(pool)
	now := time.Now().UTC()
	mk := func(label string) createVoucherRecord {
		h, fp := CodeHash(tenantID, NormalizeCode(label+"-"+suffix))
		return createVoucherRecord{
			TenantID: tenantID, AdminID: 1, CodeHash: h, CodeFingerprint: fp,
			AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
			MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
		}
	}
	batch, vouchers, err := store.CreateBatch(ctx, createBatchRecord{
		TenantID: tenantID, AdminID: 1, RequestedCount: 2, AmountCents: 500, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour), MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	}, []createVoucherRecord{mk("B1"), mk("B2")})
	if err != nil {
		t.Fatalf("create batch on PG: %v", err)
	}
	if batch.ID == 0 {
		t.Fatalf("batch id = 0, want assigned")
	}
	if len(vouchers) != 2 {
		t.Fatalf("batch vouchers = %d, want 2", len(vouchers))
	}
	for _, v := range vouchers {
		if v.Status != StatusActive {
			t.Fatalf("batch voucher status = %q, want active", v.Status)
		}
	}
	if n := countVouchers(t, ctx, pool, tenantID); n != 2 {
		t.Fatalf("batch vouchers persisted = %d, want 2", n)
	}
}
