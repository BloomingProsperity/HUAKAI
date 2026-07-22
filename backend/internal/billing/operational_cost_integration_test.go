//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestOperationalCostReserveAndAbortNeverRequireUserBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "operational-reserve")
	if _, err := pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID); err != nil {
		t.Fatalf("删除普通用户余额行失败：%v", err)
	}

	req := baseRequest(tenantID, apiKeyID, userID)
	req.BillingEffect = BillingEffectOperationalCost
	reserved, err := NewClaimGate(pool).Reserve(ctx, req)
	if err != nil {
		t.Fatalf("运营成本预留失败：%v", err)
	}
	var effect string
	var holdCount int
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM billing_ledger_claims WHERE id=$1`, reserved.ClaimID).Scan(&effect); err != nil {
		t.Fatalf("读取 claim 资金效果失败：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM balance_holds WHERE claim_id=$1`, reserved.ClaimID).Scan(&holdCount); err != nil {
		t.Fatalf("统计余额预扣失败：%v", err)
	}
	if effect != string(BillingEffectOperationalCost) || holdCount != 0 {
		t.Fatalf("资金效果=%q，余额预扣=%d；期望 operational_cost/0", effect, holdCount)
	}

	if err := NewSettler(pool).Abort(ctx, tenantID, reserved.ClaimID, "operator_cancelled", "hermes-abort", 0, nil); err != nil {
		t.Fatalf("中止运营成本 claim 失败：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted'`, reserved.ClaimID).Scan(&effect); err != nil {
		t.Fatalf("读取中止事件资金效果失败：%v", err)
	}
	if effect != string(BillingEffectOperationalCost) {
		t.Fatalf("中止事件资金效果=%q", effect)
	}
}

func TestOperationalCostSettlePersistsEvidenceWithoutBalanceMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "operational-settle")
	if _, err := pool.Exec(ctx, `UPDATE billing_ledger_claims SET billing_effect='operational_cost' WHERE id=$1`, seed.claimID); err != nil {
		t.Fatalf("设置运营成本 claim 失败：%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("删除普通用户余额行失败：%v", err)
	}

	req := settleRequest(seed, decimal.RequireFromString("0.03125000"))
	req.BillingEffect = BillingEffectOperationalCost
	result, err := NewSettler(pool).Settle(ctx, req)
	if err != nil {
		t.Fatalf("结算运营成本失败：%v", err)
	}
	if result.BillingEffect != BillingEffectOperationalCost || result.BalanceChanged || !result.NewUserBalance.IsZero() {
		t.Fatalf("结算结果=%+v；运营成本不得改变用户余额", result)
	}

	var claimEffect, usageEffect, eventEffect string
	var actualCost decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT billing_effect, actual_cost FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&claimEffect, &actualCost); err != nil {
		t.Fatalf("读取 claim 证据失败：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM usage_records WHERE claim_id=$1`, seed.claimID).Scan(&usageEffect); err != nil {
		t.Fatalf("读取用量证据失败：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'`, seed.claimID).Scan(&eventEffect); err != nil {
		t.Fatalf("读取账务事件证据失败：%v", err)
	}
	if claimEffect != string(BillingEffectOperationalCost) || usageEffect != claimEffect || eventEffect != claimEffect {
		t.Fatalf("资金效果不一致：claim=%q usage=%q event=%q", claimEffect, usageEffect, eventEffect)
	}
	if !actualCost.Equal(decimal.RequireFromString("0.03125000")) {
		t.Fatalf("实际运营成本=%s", actualCost)
	}

	refundReq := RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 1,
		Reason: "不应退款", IdempotencyKey: "operational-no-refund", AuditRequestID: "hermes-refund",
	}
	if err := NewSettler(pool).VerifyRefundableCharge(ctx, refundReq); !errors.Is(err, ErrRefundNoCapturedCharge) {
		t.Fatalf("退款资格错误=%v，期望 ErrRefundNoCapturedCharge", err)
	}
	if _, err := NewSettler(pool).Refund(ctx, refundReq); !errors.Is(err, ErrRefundNoCapturedCharge) {
		t.Fatalf("退款错误=%v，期望 ErrRefundNoCapturedCharge", err)
	}
}

func TestOperationalCostCacheHitKeepsEffectAndSkipsBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "operational-cache")
	if _, err := pool.Exec(ctx, `
UPDATE billing_ledger_claims
SET billing_effect='operational_cost', provider_account_id=NULL, acquisition_token=NULL
WHERE id=$1`, seed.claimID); err != nil {
		t.Fatalf("准备缓存命中 claim 失败：%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE claim_id=$1`, seed.claimID); err != nil {
		t.Fatalf("删除未使用槽位失败：%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("删除普通用户余额行失败：%v", err)
	}
	req := settleRequest(seed, decimal.Zero)
	req.BillingEffect = BillingEffectOperationalCost
	if err := NewSettler(pool).CommitCacheHit(ctx, req); err != nil {
		t.Fatalf("提交运营成本缓存命中失败：%v", err)
	}
	var usageEffect, eventEffect string
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM usage_records WHERE claim_id=$1`, seed.claimID).Scan(&usageEffect); err != nil {
		t.Fatalf("读取缓存用量证据失败：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT billing_effect FROM billing_events WHERE claim_id=$1`, seed.claimID).Scan(&eventEffect); err != nil {
		t.Fatalf("读取缓存事件证据失败：%v", err)
	}
	if usageEffect != string(BillingEffectOperationalCost) || eventEffect != usageEffect {
		t.Fatalf("缓存命中资金效果不一致：usage=%q event=%q", usageEffect, eventEffect)
	}
}
