//go:build integration_pg

package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const disputeRefundCostMicroUSD int64 = 12345

var disputeRefundCostUSD = decimal.RequireFromString("0.01234500")

// 变异：删除 ResolveDispute 中的 RefundInTx 调用。
// 状态可能仍变为 resolved，但余额与负向账务事件断言都会变红。
func TestAT_AUDIT_001_056_ResolvedDisputeRefundsLedgerAndBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "resolved", "committed")
	resolver := newDisputeRefundResolver(t, pool)

	result, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if result.Dispute.Status != DisputeStatusResolved || result.RefundMicroUSD != disputeRefundCostMicroUSD || result.RefundIdempotent {
		t.Fatalf("result=%+v, want resolved refund=%d non-idempotent", result, disputeRefundCostMicroUSD)
	}
	if !strings.HasPrefix(result.RefundAdjustmentRef, "billing_event:") {
		t.Fatalf("adjustment_ref=%q, want billing_event reference", result.RefundAdjustmentRef)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))

	store, err := NewPGXDisputeStore(pool)
	if err != nil {
		t.Fatalf("NewPGXDisputeStore: %v", err)
	}
	rows, err := store.ListForAdmin(ctx, f.tenantID, DisputeStatusResolved, 10, 0)
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != f.disputeID || rows[0].RefundedMicroUSD != disputeRefundCostMicroUSD {
		t.Fatalf("admin rows=%+v, want refunded_micro_usd=%d", rows, disputeRefundCostMicroUSD)
	}
}

// 变异：删除 ResolveCostDispute 的 open/reviewing WHERE 守卫。
// 第二次裁决会成功进入退款路径，本测试的 typed conflict 与单笔事件断言都会变红。
func TestAT_AUDIT_001_057_TerminalDisputeCannotBeResolvedTwice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "terminal", "committed")
	resolver := newDisputeRefundResolver(t, pool)

	if _, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved)); err != nil {
		t.Fatalf("first ResolveDispute: %v", err)
	}
	_, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusRejected))
	if !errors.Is(err, ErrDisputeNotResolvable) {
		t.Fatalf("second ResolveDispute err=%v, want ErrDisputeNotResolvable", err)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))
}

// 变异：rejected 分支也调用退款执行器。
// 负向事件数或余额任一变化都会使本测试变红。
func TestAT_AUDIT_001_058_RejectedDisputeDoesNotTouchMoney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "rejected", "committed")
	resolver := newDisputeRefundResolver(t, pool)

	result, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusRejected))
	if err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if result.RefundMicroUSD != 0 || result.RefundAdjustmentRef != "" || result.RefundIdempotent {
		t.Fatalf("rejected result unexpectedly contains refund: %+v", result)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusRejected, 0, decimal.RequireFromString("9.98765500"))
}

// 变异：查不到 committed claim 时跳过退款并照常提交 resolved。
// 明确错误与 open 状态断言共同证明失败整笔回滚、运营仍可修复后重试。
func TestAT_AUDIT_001_059_MissingCommittedClaimRollsBackResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "no-committed", "reserving")
	resolver := newDisputeRefundResolver(t, pool)

	_, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if !errors.Is(err, ErrDisputeNoCharge) {
		t.Fatalf("ResolveDispute err=%v, want ErrDisputeNoCharge", err)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusOpen, 0, decimal.RequireFromString("10.00000000"))
}

// 同租户不同用户可以使用相同 request_id，争议退款必须绑定争议所属用户，不能按最新 claim 误退他人。
func TestResolvedDisputeRefundsOnlyDisputeOwnerClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "same-request-other-user", "committed")
	otherUserID, otherClaimID := seedAdditionalDisputeClaim(t, ctx, pool, f, 0)
	resolver := newDisputeRefundResolver(t, pool)

	result, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if result.RefundMicroUSD != disputeRefundCostMicroUSD {
		t.Fatalf("refund_micro_usd=%d want %d", result.RefundMicroUSD, disputeRefundCostMicroUSD)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))

	var otherEvents int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM billing_events
WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`,
		f.tenantID, otherClaimID).Scan(&otherEvents); err != nil {
		t.Fatalf("count other user refund events: %v", err)
	}
	if otherEvents != 0 {
		t.Fatalf("other user refund events=%d want 0", otherEvents)
	}
	var otherBalance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, otherUserID).Scan(&otherBalance); err != nil {
		t.Fatalf("read other user balance: %v", err)
	}
	if !otherBalance.Equal(decimal.RequireFromString("9.98765500")) {
		t.Fatalf("other user balance=%s want 9.98765500", otherBalance)
	}
}

// 历史异常若给同一用户和 request_id 留下多条 committed claim，自动退款必须停下人工消歧。
func TestResolvedDisputeRejectsAmbiguousOwnerClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "ambiguous-owner-claims", "committed")
	_, secondClaimID := seedAdditionalDisputeClaim(t, ctx, pool, f, f.userID)
	resolver := newDisputeRefundResolver(t, pool)

	_, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if !errors.Is(err, ErrDisputeAmbiguousCharge) {
		t.Fatalf("ResolveDispute err=%v want ErrDisputeAmbiguousCharge", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM cost_disputes WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.disputeID).Scan(&status); err != nil {
		t.Fatalf("read dispute status: %v", err)
	}
	if status != DisputeStatusOpen {
		t.Fatalf("dispute status=%q want open", status)
	}
	var events, operations int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id IN ($2, $3)
        AND event_type='reconciliation_appended' AND actual_cost_signed < 0),
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id IN ($2, $3))`,
		f.tenantID, f.claimID, secondClaimID).Scan(&events, &operations); err != nil {
		t.Fatalf("read ambiguous refund facts: %v", err)
	}
	if events != 0 || operations != 0 {
		t.Fatalf("events=%d operations=%d want 0/0", events, operations)
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID).Scan(&balance); err != nil {
		t.Fatalf("read owner balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.97531000")) {
		t.Fatalf("owner balance=%s want 9.97531000", balance)
	}
}

func TestResolvedDisputeQuotaFailureRollsBackAndCanRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "quota-rollback", "committed")
	want := errors.New("dispute quota reverse failed")
	quotaReverser := &refundAtomicQuotaReverser{err: want, requireOperationFact: true}
	resolver, err := NewCostDisputeResolver(pool, billing.NewSettler(pool), WithDisputeQuotaReverser(quotaReverser))
	if err != nil {
		t.Fatalf("NewCostDisputeResolver: %v", err)
	}

	_, err = resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if !errors.Is(err, want) {
		t.Fatalf("ResolveDispute error=%v want %v", err, want)
	}
	if quotaReverser.txCalls != 1 || quotaReverser.legacyCalls != 0 || quotaReverser.lastAmount != disputeRefundCostMicroUSD {
		t.Fatalf("quota calls tx/legacy/amount=%d/%d/%d", quotaReverser.txCalls, quotaReverser.legacyCalls, quotaReverser.lastAmount)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusOpen, 0, decimal.RequireFromString("9.98765500"))

	quotaReverser.err = nil
	quotaReverser.txCalls = 0
	result, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if err != nil {
		t.Fatalf("retry ResolveDispute: %v", err)
	}
	if result.RefundMicroUSD != disputeRefundCostMicroUSD || quotaReverser.txCalls != 1 || quotaReverser.legacyCalls != 0 {
		t.Fatalf("retry result=%+v quota calls tx/legacy=%d/%d", result, quotaReverser.txCalls, quotaReverser.legacyCalls)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))
}

func TestResolvedDisputeWithoutCapturedChargeRemainsOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "no-captured-charge", "committed")
	if _, err := pool.Exec(ctx, `DELETE FROM balance_holds WHERE claim_id=$1`, f.claimID); err != nil {
		t.Fatalf("remove captured charge evidence: %v", err)
	}
	resolver := newDisputeRefundResolver(t, pool)

	_, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if !errors.Is(err, ErrDisputeNoCharge) {
		t.Fatalf("ResolveDispute err=%v want ErrDisputeNoCharge", err)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusOpen, 0, decimal.RequireFromString("9.98765500"))
}

// 变异：去掉状态行守卫、claim 行锁或退款审计幂等键任一层。
// 两个 goroutine 同时裁决时，成功数、事件数或余额至少一项会偏离一次。
func TestAT_AUDIT_001_060_ConcurrentResolutionRefundsExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "concurrent", "committed")
	resolver := newDisputeRefundResolver(t, pool)

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
			results <- err
		}()
	}
	close(start)

	var success, terminal int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrDisputeNotResolvable):
			terminal++
		default:
			t.Fatalf("concurrent ResolveDispute unexpected err=%v", err)
		}
	}
	if success != 1 || terminal != 1 {
		t.Fatalf("concurrent outcomes success=%d terminal=%d, want 1/1", success, terminal)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))
}

// 变异：RefundInTx 的 audit_request_id 幂等命中仍再次写事件或回补余额。
// 直接重放组合层退款段，要求返回存储结果且账务效果保持单笔。
func TestAT_AUDIT_001_061_RefundSegmentReplayIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeRefundPool(t, ctx)
	f := seedDisputeRefundFixture(t, ctx, pool, "replay", "committed")
	resolver := newDisputeRefundResolver(t, pool)

	first, err := resolver.ResolveDispute(ctx, f.resolveInput(DisputeStatusResolved))
	if err != nil {
		t.Fatalf("first ResolveDispute: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin replay tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replay, err := resolver.refundResolvedDisputeInTx(ctx, tx, first.Dispute)
	if err != nil {
		t.Fatalf("refund segment replay: %v", err)
	}
	if !replay.Idempotent || replay.RefundMicroUSD != first.RefundMicroUSD || replay.AdjustmentRef != first.RefundAdjustmentRef {
		t.Fatalf("replay=%+v first=%+v, want stored idempotent result", replay, first)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit replay tx: %v", err)
	}
	assertDisputeRefundState(t, ctx, pool, f, DisputeStatusResolved, 1, decimal.RequireFromString("10.00000000"))
}

type disputeRefundFixture struct {
	tenantID  int64
	userID    int64
	apiKeyID  int64
	claimID   int64
	disputeID int64
	publicID  string
	requestID string
}

func (f disputeRefundFixture) resolveInput(status string) ResolveCostDisputeInput {
	return ResolveCostDisputeInput{
		TenantID:     f.tenantID,
		ID:           f.disputeID,
		Status:       status,
		OperatorNote: "money test " + status,
	}
}

func openDisputeRefundPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	if dsn == "" {
		t.Fatal("HUAKAI_DATABASE_URL 必须设置；money 集成测试禁止跳过真 PostgreSQL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedDisputeRefundFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label, claimStatus string) disputeRefundFixture {
	t.Helper()
	suffix := fmt.Sprintf("dispute-refund-%s-%d", label, time.Now().UnixNano())
	f := disputeRefundFixture{
		publicID:  "disp_" + suffix,
		requestID: "req_" + suffix,
	}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, f.tenantID, "user-"+suffix).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active')
RETURNING id`, f.tenantID, f.userID, "key-"+suffix, "$2a$10$dispute-refund-placeholder", "hk_test_"+suffix[:8]).Scan(&f.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	initialBalance := decimal.RequireFromString("10.00000000")
	if claimStatus == "committed" {
		initialBalance = initialBalance.Sub(disputeRefundCostUSD)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, $3, 0)`, f.tenantID, f.userID, initialBalance); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	settledAt := any(time.Now().UTC())
	if claimStatus != "committed" {
		settledAt = nil
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
    logical_request_id, endpoint_family, requested_model, billing_policy_version,
    request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
    lease_expires_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, 'chat', 'gpt-4o', '1.0',
    'standard', $7, $7, 'USD', $8, $9,
    now() + interval '5 minutes'
) RETURNING id`, f.tenantID, "idem-"+suffix, "fingerprint-"+suffix, f.apiKeyID, f.userID,
		f.requestID, disputeRefundCostUSD, claimStatus, settledAt).Scan(&f.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if claimStatus == "committed" {
		if _, err := pool.Exec(ctx, `INSERT INTO balance_holds (claim_id, tenant_id, user_id, amount, captured, state, resolved_at) VALUES ($1, $2, $3, $4, $4, 'captured', $5)`, f.claimID, f.tenantID, f.userID, disputeRefundCostUSD, settledAt); err != nil {
			t.Fatalf("seed captured balance hold: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO cost_disputes (dispute_id, tenant_id, user_id, request_id, reason, status)
VALUES ($1, $2, $3, $4, 'cost is wrong', 'open')
RETURNING id`, f.publicID, f.tenantID, f.userID, f.requestID).Scan(&f.disputeID); err != nil {
		t.Fatalf("seed dispute: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_refund_operations WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_events WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cost_disputes WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM balance_holds WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
	})
	return f
}

func seedAdditionalDisputeClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f disputeRefundFixture, userID int64) (int64, int64) {
	t.Helper()
	suffix := fmt.Sprintf("additional-dispute-claim-%d", time.Now().UnixNano())
	if userID <= 0 {
		if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, f.tenantID, "user-"+suffix).Scan(&userID); err != nil {
			t.Fatalf("seed additional user: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, $3, 0)`,
			f.tenantID, userID, decimal.RequireFromString("10.00000000").Sub(disputeRefundCostUSD)); err != nil {
			t.Fatalf("seed additional user balance: %v", err)
		}
	} else if _, err := pool.Exec(ctx, `UPDATE user_balances SET balance=balance-$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND user_id=$2`,
		f.tenantID, userID, disputeRefundCostUSD); err != nil {
		t.Fatalf("debit duplicate owner charge: %v", err)
	}

	var apiKeyID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active')
RETURNING id`, f.tenantID, userID, "key-"+suffix, "$2a$10$additional-dispute-placeholder", "hk_more_"+suffix[len(suffix)-8:]).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed additional api key: %v", err)
	}

	var claimID int64
	settledAt := time.Now().UTC()
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
    logical_request_id, endpoint_family, requested_model, billing_policy_version,
    request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
    lease_expires_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, 'chat', 'gpt-4o', '1.0',
    'standard', $7, $7, 'USD', 'committed', $8,
    now() + interval '5 minutes'
) RETURNING id`, f.tenantID, "idem-"+suffix, "fingerprint-"+suffix, apiKeyID, userID,
		f.requestID, disputeRefundCostUSD, settledAt).Scan(&claimID); err != nil {
		t.Fatalf("seed additional claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO balance_holds (claim_id, tenant_id, user_id, amount, captured, state, resolved_at)
VALUES ($1, $2, $3, $4, $4, 'captured', $5)`, claimID, f.tenantID, userID, disputeRefundCostUSD, settledAt); err != nil {
		t.Fatalf("seed additional captured hold: %v", err)
	}
	return userID, claimID
}

func newDisputeRefundResolver(t *testing.T, pool *pgxpool.Pool) *CostDisputeResolver {
	t.Helper()
	resolver, err := NewCostDisputeResolver(pool, billing.NewSettler(pool))
	if err != nil {
		t.Fatalf("NewCostDisputeResolver: %v", err)
	}
	return resolver
}

func assertDisputeRefundState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f disputeRefundFixture, wantStatus string, wantRefundEvents int64, wantBalance decimal.Decimal) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM cost_disputes WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.disputeID).Scan(&status); err != nil {
		t.Fatalf("read dispute status: %v", err)
	}
	if status != wantStatus {
		t.Fatalf("dispute status=%q want %q", status, wantStatus)
	}
	var events int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM billing_events
	WHERE tenant_id=$1
	  AND claim_id=$2
	  AND event_type='reconciliation_appended'
	  AND actual_cost_signed < 0
	  AND end_class='cost_dispute'
	  AND usage_source='cost_dispute'
	  AND audit_request_id=$3`, f.tenantID, f.claimID, disputeRefundAuditRequestID(f.publicID)).Scan(&events); err != nil {
		t.Fatalf("count dispute refund events: %v", err)
	}
	if events != wantRefundEvents {
		t.Fatalf("refund events=%d want %d", events, wantRefundEvents)
	}
	var operations int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2`, f.tenantID, f.claimID).Scan(&operations); err != nil {
		t.Fatalf("count dispute refund operations: %v", err)
	}
	if operations != wantRefundEvents {
		t.Fatalf("refund operations=%d want %d", operations, wantRefundEvents)
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID).Scan(&balance); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(wantBalance) {
		t.Fatalf("balance=%s want %s", balance, wantBalance)
	}
}
