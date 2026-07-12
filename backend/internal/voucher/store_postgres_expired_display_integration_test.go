//go:build integration_pg

package voucher

import (
	"context"
	"testing"
	"time"
)

// TestListVouchersRecomputesExpiredStatus 守护:过期券的惰性物化 UPDATE(status='expired')在兑换
// 事务里总被回滚,DB status 列对"时间已过期但仍 'active'"的券滞留 'active';ListVouchers 必须按
// valid_until 读时重算,把这类券显示为 'expired'(admin 列表不再误显示为可用)。未过期的 active 券
// 必须保持 active,不被误判。
// 判别:删去 SELECT 里的 CASE 读时重算 → 过期券读回 'active' → 本测试转红。
func TestListVouchersRecomputesExpiredStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openVoucherPool(t, ctx)

	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"voucher-exp-"+time.Now().Format("150405.000000000")).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM voucher_batch WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	vf := time.Now().Add(-48 * time.Hour)
	var batchID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO voucher_batch (tenant_id, created_by_admin_id, requested_count, created_count, amount_cents, valid_from, valid_until)
VALUES ($1,1,2,2,$2,$3,$4) RETURNING id`, tenantID, 500, vf, time.Now().Add(24*time.Hour)).Scan(&batchID); err != nil {
		t.Fatalf("insert batch: %v", err)
	}

	// 直接以 status='active' 写库(模拟惰性物化从未生效),分别给过期/未过期两张券。
	insertVoucher := func(suffix string, validUntil time.Time) {
		if _, err := pool.Exec(ctx, `
INSERT INTO voucher (tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, valid_from, valid_until, status, revoked_reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,'active','')`,
			tenantID, batchID,
			[]byte("vexp-h-"+suffix), "vexp-fp-"+suffix, 500, vf, validUntil); err != nil {
			t.Fatalf("insert voucher %s: %v", suffix, err)
		}
	}
	insertVoucher("expired", time.Now().Add(-time.Hour)) // 时间已过期
	insertVoucher("active", time.Now().Add(time.Hour))   // 未过期

	store := NewPostgresStore(pool)
	vouchers, err := store.ListVouchers(ctx, ListInput{TenantID: tenantID, Limit: 100})
	if err != nil {
		t.Fatalf("ListVouchers: %v", err)
	}
	var sawExpired, sawActive bool
	for _, v := range vouchers {
		switch v.CodeFingerprint {
		case "vexp-fp-expired":
			sawExpired = true
			if v.Status != StatusExpired {
				t.Fatalf("时间已过期的券应读时重算为 expired, 实际 %q", v.Status)
			}
		case "vexp-fp-active":
			sawActive = true
			if v.Status != StatusActive {
				t.Fatalf("未过期的券应保持 active(不被误判过期), 实际 %q", v.Status)
			}
		}
	}
	if !sawExpired || !sawActive {
		t.Fatalf("两张测试券都应被 ListVouchers 返回, expired=%v active=%v", sawExpired, sawActive)
	}
}
