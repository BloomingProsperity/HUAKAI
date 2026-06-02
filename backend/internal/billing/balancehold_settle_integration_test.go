//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/balancehold"
)

func TestSettler_SettleAppliesCaptureAndReturnsUpdatedBalance(t *testing.T) {
	// Mutation check: capture path writes usage/billing row but never debits user balance.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-hold-success")
	set := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}

	res, err := set.Settle(ctx, settleRequest(seed, decimal.NewFromFloat(0.03)))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !res.NewUserBalance.Equal(decimal.RequireFromString("9.97")) {
		t.Fatalf("NewUserBalance=%s want 9.97", res.NewUserBalance)
	}

	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.97")) || !held.Equal(decimal.Zero) {
		t.Fatalf("after settle balance=%s held=%s want 9.97/0", balance, held)
	}
}

func TestSettler_CacheHitCapturesZeroAndReleasesHold(t *testing.T) {
	// Mutation check: skip BalanceHold.Capture in CommitCacheHit.
	// Without capture, held would remain 0.01 on a success L2 hit.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "cache-hit-hold")
	set := NewSettler(pool)
	if _, err := pool.Exec(ctx, `UPDATE billing_ledger_claims SET provider_account_id = NULL, acquisition_token = NULL WHERE id=$1`, seed.claimID); err != nil {
		t.Fatalf("clear provider state: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}

	if err := set.CommitCacheHit(ctx, settleRequest(seed, decimal.Zero)); err != nil {
		t.Fatalf("CommitCacheHit: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "committed" {
		t.Fatalf("status=%q want committed", status)
	}

	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("after cache-hit balance=%s held=%s want 10.00/0", balance, held)
	}
}

func TestSettler_AbortReleasesHold(t *testing.T) {
	// Mutation check: omit Release in Abort.
	// Without Release the held budget remains non-zero and future claims overdraw.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "abort-release-hold")
	set := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}

	if err := set.Abort(ctx, seed.tenantID, seed.claimID, "abort-hold", fmt.Sprintf("req-abort-hold-%d", seed.claimID), 0, nil); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "aborted" {
		t.Fatalf("status=%q want aborted", status)
	}
	var held decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&held); err != nil {
		t.Fatalf("read held: %v", err)
	}
	if !held.Equal(decimal.Zero) {
		t.Fatalf("held=%s want 0", held)
	}
}

func TestSettler_LeaseSweepAbortsExpiredClaims(t *testing.T) {
	// Mutation check: sweep selects only stale claims or never aborts them.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	set := NewSettler(pool)
	sweeper := NewLeaseSweeper(pool, set, 10)
	seed := seedSettlerGraph(t, ctx, pool, "lease-sweep")

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_ledger_claims SET lease_expires_at = NOW() - interval '100 years' WHERE id=$1`, seed.claimID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	count, err := sweeper.SweepOnce(ctx)
	if err != nil {
		// sweep 已逐 claim 容错续扫;共享 dev 库的历史孤儿可能引发个别 per-claim
		// 错误,但不应中断本批。本测试只关心 seeded claim 是否被回收(下方强断言)。
		t.Logf("SweepOnce non-fatal per-claim errors (shared dev DB orphans): %v", err)
	}
	if count == 0 {
		t.Fatalf("SweepOnce=%d want at least 1 aborted claim", count)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "aborted" {
		t.Fatalf("status=%q want aborted", status)
	}
}

func TestSettler_LeaseSweepReclaimsExpiredSlotAcquisitions(t *testing.T) {
	// Mutation check: remove orphan slot sweeping and the slot stays acquired with in_flight_count=1.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	set := NewSettler(pool)
	sweeper := NewLeaseSweeper(pool, set, 10)
	graph := seedRetryAtomicityGraph(t, ctx, pool, "slot-orphan-sweep")
	token := uuid.New()

	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET in_flight_count=1 WHERE id=$1`,
		graph.firstAccountID,
	); err != nil {
		t.Fatalf("seed in_flight_count: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES ($1, $2, $3, NULL, 1, NOW() - interval '100 years')`,
		graph.tenantID, graph.firstAccountID, token,
	); err != nil {
		t.Fatalf("seed expired slot acquisition: %v", err)
	}

	count, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Logf("SweepOnce non-fatal errors from shared dev DB: %v", err)
	}
	if count == 0 {
		t.Fatalf("SweepOnce=%d want at least one reclaimed lease or slot", count)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM pool_slot_acquisitions WHERE acquisition_token=$1`,
		token,
	).Scan(&status); err != nil {
		t.Fatalf("read slot status: %v", err)
	}
	if status != "orphan_swept" {
		t.Fatalf("slot status=%q want orphan_swept", status)
	}

	var inFlight int32
	if err := pool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`,
		graph.firstAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight_count: %v", err)
	}
	if inFlight != 0 {
		t.Fatalf("in_flight_count=%d want 0 after orphan slot sweep", inFlight)
	}
}

func TestSettler_LeaseSweepReclaimsExpiredSlotFromPriorAttempt(t *testing.T) {
	// Mutation check: remove attempt_seq from the live-claim guard and the prior-attempt slot remains acquired.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	set := NewSettler(pool)
	sweeper := NewLeaseSweeper(pool, set, 10)
	graph := seedRetryAtomicityGraph(t, ctx, pool, "slot-prior-attempt-sweep")
	oldToken := uuid.New()
	liveToken := uuid.New()

	var claimID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', $8, $9,
			2, $10, 'USD', NOW() + interval '90 seconds'
		) RETURNING id`,
		graph.tenantID, "idempotency-"+graph.fingerprint, graph.fingerprint, graph.apiKeyID, graph.userID,
		"logical-"+graph.fingerprint, graph.secondPoolID, graph.secondAccountID, liveToken,
		decimal.RequireFromString("0.01000000"),
	).Scan(&claimID); err != nil {
		t.Fatalf("seed live re-reserved claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET in_flight_count=1 WHERE id IN ($1, $2)`,
		graph.firstAccountID, graph.secondAccountID,
	); err != nil {
		t.Fatalf("seed in_flight_count: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES
			($1, $2, $3, $4, 1, NOW() - interval '100 years'),
			($1, $5, $6, $4, 2, NOW() - interval '100 years')`,
		graph.tenantID, graph.firstAccountID, oldToken, claimID, graph.secondAccountID, liveToken,
	); err != nil {
		t.Fatalf("seed old and live slot acquisitions: %v", err)
	}

	count, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Logf("SweepOnce non-fatal errors from shared dev DB: %v", err)
	}
	if count == 0 {
		t.Fatalf("SweepOnce=%d want prior-attempt slot reclaimed", count)
	}

	var oldStatus, liveStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM pool_slot_acquisitions WHERE acquisition_token=$1`,
		oldToken,
	).Scan(&oldStatus); err != nil {
		t.Fatalf("read old slot status: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM pool_slot_acquisitions WHERE acquisition_token=$1`,
		liveToken,
	).Scan(&liveStatus); err != nil {
		t.Fatalf("read live slot status: %v", err)
	}
	if oldStatus != "orphan_swept" {
		t.Fatalf("old attempt slot status=%q want orphan_swept", oldStatus)
	}
	if liveStatus != "acquired" {
		t.Fatalf("live attempt slot status=%q want acquired", liveStatus)
	}

	assertAccountInFlight(t, ctx, pool, graph.firstAccountID, 0)
	assertAccountInFlight(t, ctx, pool, graph.secondAccountID, 1)
}

func TestSettler_RefundCreditsUserBalance(t *testing.T) {
	// Mutation check: remove refund UPDATE to user_balances.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-credit")
	set := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_ledger_claims SET status='committed', actual_cost=$2 WHERE id=$1`, seed.claimID, decimal.RequireFromString("0.02000000")); err != nil {
		t.Fatalf("set committed: %v", err)
	}

	res, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7000,
		Reason:         "audit_mismatch",
		AuditRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if res.RefundMicroUSD != 7000 {
		t.Fatalf("refund micros=%d want 7000", res.RefundMicroUSD)
	}

	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00700000")) {
		t.Fatalf("balance=%s want 10.00700000", balance)
	}
}

func reserveAndCommitBalanceHold(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenantID, userID, claimID int64, cost decimal.Decimal) error {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := balancehold.Reserve(ctx, tx, balancehold.ReserveParams{TenantID: tenantID, UserID: userID, ClaimID: claimID, Cost: cost}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
