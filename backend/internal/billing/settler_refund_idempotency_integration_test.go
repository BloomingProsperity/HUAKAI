//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// 守 P2(退款幂等-存储金额):同 audit_request_id 的退款重放(即使传入不同金额)必须返回**存储的**
// 退款额,而非调用方新传入的金额,且余额只回补一次。该测试也覆盖锁前置后的重排路径(第一次退款
// 提交后,第二次在持锁的幂等 SELECT 命中)。
// Mutation: 幂等 hit 返回 req.AmountMicroUSD(原 bug)-> 第二次返回 9999 -> 红。
func TestSettler_RefundIdempotentReplayReturnsStoredAmount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-stored")
	set := NewSettler(pool)
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_ledger_claims SET status='committed', actual_cost=$2 WHERE id=$1`, seed.claimID, decimal.RequireFromString("0.02000000")); err != nil {
		t.Fatalf("commit claim: %v", err)
	}
	auditID := uuid.NewString()
	res1, err := set.Refund(ctx, RefundRequest{TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 7000, Reason: "audit_mismatch", AuditRequestID: auditID})
	if err != nil || res1.RefundMicroUSD != 7000 {
		t.Fatalf("first refund: res=%+v err=%v", res1, err)
	}
	// 重放: 同 auditID, 故意传不同金额 9999
	res2, err := set.Refund(ctx, RefundRequest{TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 9999, Reason: "audit_mismatch", AuditRequestID: auditID})
	if err != nil {
		t.Fatalf("replay refund: %v", err)
	}
	if !res2.Idempotent {
		t.Fatal("replay must be Idempotent=true")
	}
	if res2.RefundMicroUSD != 7000 {
		t.Fatalf("replay refund micros=%d want 7000 (stored amount, not the replay's 9999)", res2.RefundMicroUSD)
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00700000")) {
		t.Fatalf("balance=%s want 10.00700000 (refund credited exactly once, not twice)", balance)
	}
}
