//go:build integration_pg

package voucher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestRedeemCreditsUserBalanceForReserveCaptureAndIdempotency(t *testing.T) {
	// Mutation check: remove the voucher redemption -> user_balances UPSERT.
	// Without that bridge, the first balance read has no row and Reserve pass-throughs
	// without creating a hold, so the reserve/capture assertions fail.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-replay",
		SourceIP:       "203.0.113.1",
		RequestID:      "req-money-bridge-1",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after first redeem", decimal.NewFromInt(100), decimal.Zero)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-replay",
		SourceIP:       "203.0.113.1",
		RequestID:      "req-money-bridge-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("redeem replay: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("replay Idempotent=false, want true")
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after replay", decimal.NewFromInt(100), decimal.Zero)

	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	reserved, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, decimal.NewFromInt(100))
	if err != nil {
		t.Fatalf("reserve redeemed balance: %v", err)
	}
	if !reserved.Balance.Equal(decimal.NewFromInt(100)) || !reserved.Held.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("reserve snapshot balance=%s held=%s want 100/100", reserved.Balance, reserved.Held)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after reserve", decimal.NewFromInt(100), decimal.NewFromInt(100))

	settled, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, decimal.NewFromInt(100)))
	if err != nil {
		t.Fatalf("settle redeemed balance: %v", err)
	}
	if !settled.NewUserBalance.Equal(decimal.Zero) {
		t.Fatalf("settled NewUserBalance=%s want 0", settled.NewUserBalance)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after capture", decimal.Zero, decimal.Zero)

	afterSpendReplay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-replay",
		SourceIP:       "203.0.113.1",
		RequestID:      "req-money-bridge-after-spend-replay",
		Now:            now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("redeem replay after capture: %v", err)
	}
	if !afterSpendReplay.Idempotent {
		t.Fatalf("after-spend replay Idempotent=false, want true")
	}
	if afterSpendReplay.BalanceCents != 0 {
		t.Fatalf("after-spend replay balance cents=%d want current wallet 0", afterSpendReplay.BalanceCents)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after spent replay", decimal.Zero, decimal.Zero)
}

func TestRedeemReplayReturnsCurrentWalletAfterPartialCapture(t *testing.T) {
	// Mutation check: make the idempotent redeem result read voucher_redemption
	// totals again. After a $30 capture, that broken path returns the original
	// 10000c voucher total instead of the current 7000c spendable balance.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-current-wallet-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-current-wallet",
		SourceIP:       "203.0.113.18",
		RequestID:      "req-money-current-wallet",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}

	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, decimal.NewFromInt(30)); err != nil {
		t.Fatalf("reserve redeemed balance: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, decimal.NewFromInt(30))); err != nil {
		t.Fatalf("settle redeemed balance: %v", err)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after partial capture", decimal.NewFromInt(70), decimal.Zero)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-current-wallet",
		SourceIP:       "203.0.113.18",
		RequestID:      "req-money-current-wallet-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("redeem replay after partial capture: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("replay Idempotent=false, want true")
	}
	if replay.BalanceCents != 7000 {
		t.Fatalf("replay balance cents=%d want current wallet 7000", replay.BalanceCents)
	}
}

func TestRedeemReplayReturnsCurrentWalletAfterPriorVoucherWasSpent(t *testing.T) {
	// Mutation check: preserve the legacy cumulative voucher-redemption floor
	// even after any wallet debit exists. After spending the first $100 voucher,
	// replaying the second $100 voucher must report the current 10000c wallet
	// rather than the cumulative 20000c voucher audit total.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	firstCode := fmt.Sprintf("money-prior-spent-first-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, firstCode, now)
	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           firstCode,
		IdempotencyKey: "money-prior-spent-first",
		SourceIP:       "203.0.113.20",
		RequestID:      "req-money-prior-spent-first",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem first voucher: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("first redeem balance cents=%d want 10000", first.BalanceCents)
	}

	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, decimal.NewFromInt(100)); err != nil {
		t.Fatalf("reserve first voucher spend: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, decimal.NewFromInt(100))); err != nil {
		t.Fatalf("settle first voucher spend: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE balance_holds SET resolved_at=$3 WHERE tenant_id=$1 AND claim_id=$2`,
		tenantID, claim.id, now.Add(-time.Second),
	); err != nil {
		t.Fatalf("backdate first voucher spend: %v", err)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after spending first voucher", decimal.Zero, decimal.Zero)

	secondCode := fmt.Sprintf("money-prior-spent-second-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, secondCode, now.Add(2*time.Second))
	second, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           secondCode,
		IdempotencyKey: "money-prior-spent-second",
		SourceIP:       "203.0.113.20",
		RequestID:      "req-money-prior-spent-second",
		Now:            now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("redeem second voucher: %v", err)
	}
	if second.BalanceCents != 10000 {
		t.Fatalf("second redeem balance cents=%d want current wallet 10000", second.BalanceCents)
	}

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           secondCode,
		IdempotencyKey: "money-prior-spent-second",
		SourceIP:       "203.0.113.20",
		RequestID:      "req-money-prior-spent-second-replay",
		Now:            now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay second voucher: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("second replay Idempotent=false, want true")
	}
	if replay.BalanceCents != 10000 {
		t.Fatalf("second replay balance cents=%d want current wallet 10000", replay.BalanceCents)
	}
}

func TestRedeemRejectsNonUSDBalanceCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, _ := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-eur-%d", tenantID)
	seedVoucherMoneyVoucherCurrency(t, ctx, pool, tenantID, code, "EUR", now)

	if _, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-eur",
		SourceIP:       "203.0.113.9",
		RequestID:      "req-money-bridge-eur",
		Now:            now.Add(time.Second),
	}); err == nil {
		t.Fatal("redeem EUR voucher succeeded, want rejection before balance mutation")
	}

	var hasBalance bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_balances WHERE tenant_id=$1 AND user_id=$2)`,
		tenantID, userID,
	).Scan(&hasBalance); err != nil {
		t.Fatalf("read user balance existence: %v", err)
	}
	if hasBalance {
		t.Fatal("EUR voucher created a currencyless user_balances row")
	}
	var redemptions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM voucher_redemption WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&redemptions); err != nil {
		t.Fatalf("read redemption count: %v", err)
	}
	if redemptions != 0 {
		t.Fatalf("EUR voucher persisted %d redemptions, want rollback before mutation", redemptions)
	}
}

func TestRedeemReturnsCurrentWalletBalanceCents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, _ := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 25, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed existing user balance: %v", err)
	}

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-existing-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	result, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-existing",
		SourceIP:       "203.0.113.10",
		RequestID:      "req-money-bridge-existing",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem onto existing balance: %v", err)
	}
	if result.BalanceCents != 12500 {
		t.Fatalf("redeem balance cents=%d want current wallet 12500", result.BalanceCents)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after existing-balance redeem", decimal.NewFromInt(125), decimal.Zero)
}

func TestRedeemReplayIgnoresPostRedeemUnheldClaims(t *testing.T) {
	// Mutation check: count every later claim_committed event as a wallet debit
	// during idempotent replay reconstruction. Claims that passed through before a
	// user_balances row existed have no balance_hold, so capture is a no-op; adding
	// their cost back inflates the replayed voucher balance.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	preProvisionClaim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-unheld-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-unheld",
		SourceIP:       "203.0.113.11",
		RequestID:      "req-money-bridge-unheld",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem after pre-provision claim: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after redeem with pre-provision claim", decimal.NewFromInt(100), decimal.Zero)

	settled, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(preProvisionClaim, decimal.NewFromInt(100)))
	if err != nil {
		t.Fatalf("settle unheld pre-provision claim: %v", err)
	}
	if !settled.NewUserBalance.Equal(decimal.Zero) {
		t.Fatalf("unheld settle NewUserBalance=%s want zero snapshot", settled.NewUserBalance)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after unheld claim settle", decimal.NewFromInt(100), decimal.Zero)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-unheld",
		SourceIP:       "203.0.113.11",
		RequestID:      "req-money-bridge-unheld-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay after unheld claim settle: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("unheld replay Idempotent=false, want true")
	}
	if replay.BalanceCents != first.BalanceCents {
		t.Fatalf("unheld replay balance cents=%d want current wallet %d", replay.BalanceCents, first.BalanceCents)
	}
}

func TestRedeemReplayPreservesLegacyUnmaterializedVoucherCredit(t *testing.T) {
	// Mutation check: replay a historical voucher_redemption that has a billing
	// event but no user_balances credit. Without the legacy no-row fallback, replay
	// cannot return the historical voucher credit.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, _ := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-legacy-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	legacyID := seedLegacyVoucherRedemption(t, ctx, pool, tenantID, userID, code, "money-bridge-legacy", now)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-legacy",
		SourceIP:       "203.0.113.12",
		RequestID:      "req-money-bridge-legacy-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("legacy replay: %v", err)
	}
	if !replay.Idempotent || replay.Redemption.ID != legacyID {
		t.Fatalf("legacy replay result = %+v, want idempotent redemption %d", replay, legacyID)
	}
	if replay.BalanceCents != 10000 {
		t.Fatalf("legacy replay balance cents=%d want preserved voucher credit 10000", replay.BalanceCents)
	}
}

func TestRedeemReplayNoEventLegacyUsesCurrentWalletAfterSpend(t *testing.T) {
	// Mutation check: keep the BillingEventID<=0 shortcut returning the
	// voucher_redemption sum. Once the legacy credit has a materialized balance row
	// and a captured spend, replay must report the current 7000c wallet.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-legacy-no-event-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	legacyID := seedLegacyVoucherRedemptionWithoutBillingEvent(t, ctx, pool, tenantID, userID, code, "money-bridge-legacy-no-event", now)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 100, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed materialized legacy balance row: %v", err)
	}

	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, decimal.NewFromInt(30)); err != nil {
		t.Fatalf("reserve legacy spend: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, decimal.NewFromInt(30))); err != nil {
		t.Fatalf("settle legacy spend: %v", err)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after legacy spend", decimal.NewFromInt(70), decimal.Zero)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-legacy-no-event",
		SourceIP:       "203.0.113.21",
		RequestID:      "req-money-bridge-legacy-no-event-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("legacy no-event replay: %v", err)
	}
	if !replay.Idempotent || replay.Redemption.ID != legacyID {
		t.Fatalf("legacy no-event replay result = %+v, want idempotent redemption %d", replay, legacyID)
	}
	if replay.BalanceCents != 7000 {
		t.Fatalf("legacy no-event replay balance cents=%d want current wallet 7000", replay.BalanceCents)
	}
}

func TestRedeemReplayPreservesLegacyCreditWithLaterWalletRow(t *testing.T) {
	// Mutation check: return user_balances immediately whenever a wallet row
	// exists. For a pre-bridge redemption, a later wallet row may not include that
	// historical voucher credit; the replay must keep the legacy redemption floor
	// while no positive captured wallet debit proves spend should be visible.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-legacy-row-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	legacyID := seedLegacyVoucherRedemption(t, ctx, pool, tenantID, userID, code, "money-bridge-legacy-row", now)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 50, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed later materialized balance row: %v", err)
	}
	zeroClaim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, zeroClaim.id, decimal.Zero); err != nil {
		t.Fatalf("reserve zero-cost hold: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(zeroClaim, decimal.Zero)); err != nil {
		t.Fatalf("settle zero-cost hold: %v", err)
	}

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-legacy-row",
		SourceIP:       "203.0.113.19",
		RequestID:      "req-money-bridge-legacy-row-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("legacy row replay: %v", err)
	}
	if !replay.Idempotent || replay.Redemption.ID != legacyID {
		t.Fatalf("legacy row replay result = %+v, want idempotent redemption %d", replay, legacyID)
	}
	if replay.BalanceCents != 10000 {
		t.Fatalf("legacy row replay balance cents=%d want preserved voucher credit 10000", replay.BalanceCents)
	}
}

func TestRedeemReplayRoundsCurrentFractionalWalletBalance(t *testing.T) {
	// Mutation check: make idempotent replay read voucher_redemption totals. After
	// a fractional capture leaves the wallet at 99.985, the broken sum path returns
	// the original 10000c instead of the rounded current wallet 9999c.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-fractional-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	_, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-fractional",
		SourceIP:       "203.0.113.13",
		RequestID:      "req-money-bridge-fractional",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("fractional redeem: %v", err)
	}
	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	fractionalCost := decimal.RequireFromString("0.015")
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, fractionalCost); err != nil {
		t.Fatalf("reserve fractional redeemed balance: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, fractionalCost)); err != nil {
		t.Fatalf("settle fractional redeemed balance: %v", err)
	}

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-fractional",
		SourceIP:       "203.0.113.13",
		RequestID:      "req-money-bridge-fractional-replay",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("fractional replay: %v", err)
	}
	if replay.BalanceCents != 9999 {
		t.Fatalf("fractional replay balance cents=%d want current wallet 9999", replay.BalanceCents)
	}
}

func TestRedeemReplayReturnsCurrentWalletAfterDelayedCapture(t *testing.T) {
	// Mutation check: reconstruct the original idempotent response from billing
	// events instead of reading user_balances. A settler can insert the
	// claim_committed event before voucher redemption but resolve the balance hold
	// after the voucher response; replay must report the current wallet after that
	// capture, not the original 20000c response.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 100, 100)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed held user balance: %v", err)
	}
	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO balance_holds (claim_id, tenant_id, user_id, amount, state) VALUES ($1, $2, $3, 100, 'held')`,
		claim.id, tenantID, userID,
	); err != nil {
		t.Fatalf("seed pending hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO billing_events (
	tenant_id, claim_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint
) VALUES ($1, $2, 'claim_committed', 100, 100, 2, 0, $3)`,
		tenantID, claim.id, claim.fingerprint,
	); err != nil {
		t.Fatalf("seed early claim billing event: %v", err)
	}

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-order-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)

	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-order",
		SourceIP:       "203.0.113.14",
		RequestID:      "req-money-bridge-order",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem with early claim event: %v", err)
	}
	if first.BalanceCents != 20000 {
		t.Fatalf("redeem balance cents=%d want 20000", first.BalanceCents)
	}
	if _, err := pool.Exec(ctx, `
UPDATE balance_holds
SET state='captured', captured=100, resolved_at=$4
WHERE claim_id=$1 AND tenant_id=$2 AND user_id=$3`,
		claim.id, tenantID, userID, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("resolve delayed hold: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE user_balances SET balance=balance-100, held=held-100 WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("apply delayed capture: %v", err)
	}

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-order",
		SourceIP:       "203.0.113.14",
		RequestID:      "req-money-bridge-order-replay",
		Now:            now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay after delayed capture: %v", err)
	}
	if replay.BalanceCents != 10000 {
		t.Fatalf("replay balance cents=%d want current wallet 10000", replay.BalanceCents)
	}
}

func TestRedeemReplayIgnoresNonUSDLegacyRedemptionFloor(t *testing.T) {
	// Mutation check: remove the USD currency predicate from redemptionBalanceThrough.
	// The replay floor then counts a legacy EUR redemption as USD cents and returns
	// 15000c instead of the original USD voucher response of 10000c.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, _ := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	legacyCode := fmt.Sprintf("money-bridge-legacy-eur-%d", tenantID)
	seedVoucherMoneyVoucherCurrencyAmount(t, ctx, pool, tenantID, legacyCode, "EUR", 5000, now)
	seedLegacyVoucherRedemptionCurrencyAmount(t, ctx, pool, tenantID, userID, legacyCode, "money-bridge-legacy-eur", "EUR", 5000, now)

	code := fmt.Sprintf("money-bridge-usd-floor-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-usd-floor",
		SourceIP:       "203.0.113.15",
		RequestID:      "req-money-bridge-usd-floor",
		Now:            now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("redeem after legacy EUR redemption: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-usd-floor",
		SourceIP:       "203.0.113.15",
		RequestID:      "req-money-bridge-usd-floor-replay",
		Now:            now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay after legacy EUR redemption: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("replay Idempotent=false, want true")
	}
	if replay.BalanceCents != first.BalanceCents {
		t.Fatalf("replay balance cents=%d want original USD response %d", replay.BalanceCents, first.BalanceCents)
	}
}

func TestRedeemReplayIgnoresNonUSDLegacyVoucherDelta(t *testing.T) {
	// Mutation check: remove the USD currency predicate from the post-redemption
	// voucher_redeemed delta. With a prior wallet debit, replay must not fall back
	// to the redemption floor; counting a later EUR voucher event reconstructs
	// 5000c instead of the original USD voucher response of 10000c.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 100, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed prior user balance: %v", err)
	}
	priorClaim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, priorClaim.id, decimal.NewFromInt(100)); err != nil {
		t.Fatalf("reserve prior wallet debit: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(priorClaim, decimal.NewFromInt(100))); err != nil {
		t.Fatalf("settle prior wallet debit: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE balance_holds SET resolved_at=NOW() - interval '1 second' WHERE tenant_id=$1 AND claim_id=$2`,
		tenantID, priorClaim.id,
	); err != nil {
		t.Fatalf("backdate prior wallet debit resolution: %v", err)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after prior wallet debit", decimal.Zero, decimal.Zero)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-usd-delta-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-usd-delta",
		SourceIP:       "203.0.113.17",
		RequestID:      "req-money-bridge-usd-delta",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem after prior debit: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}

	legacyCode := fmt.Sprintf("money-bridge-later-eur-%d", tenantID)
	seedVoucherMoneyVoucherCurrencyAmount(t, ctx, pool, tenantID, legacyCode, "EUR", 5000, now.Add(2*time.Second))
	seedLegacyVoucherRedemptionCurrencyAmount(t, ctx, pool, tenantID, userID, legacyCode, "money-bridge-later-eur", "EUR", 5000, now.Add(2*time.Second))

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-usd-delta",
		SourceIP:       "203.0.113.17",
		RequestID:      "req-money-bridge-usd-delta-replay",
		Now:            now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay after later EUR voucher event: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("replay Idempotent=false, want true")
	}
	if replay.BalanceCents != first.BalanceCents {
		t.Fatalf("replay balance cents=%d want original USD response %d", replay.BalanceCents, first.BalanceCents)
	}
}

func TestRedeemReplayReturnsCurrentWalletAfterSameTimestampReconciliation(t *testing.T) {
	// Mutation check: reconstruct the idempotent replay response from voucher
	// redemptions instead of user_balances. After a same-timestamp refund moves the
	// wallet to 5000c, the broken audit-derived path still returns the original
	// 10000c voucher total.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openVoucherPool(t, ctx)
	tenantID, userID, apiKeyID := seedVoucherMoneyUser(t, ctx, pool)
	cleanupVoucherMoneyTenant(t, pool, tenantID)

	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Now().UTC()
	code := fmt.Sprintf("money-bridge-recon-tie-%d", tenantID)
	seedVoucherMoneyVoucher(t, ctx, pool, tenantID, code, now)
	first, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-recon-tie",
		SourceIP:       "203.0.113.16",
		RequestID:      "req-money-bridge-recon-tie",
		Now:            now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("redeem before tied reconciliation: %v", err)
	}
	if first.BalanceCents != 10000 {
		t.Fatalf("redeem balance cents=%d want 10000", first.BalanceCents)
	}
	var redemptionEventTime time.Time
	if err := pool.QueryRow(ctx,
		`SELECT occurred_at FROM billing_events WHERE tenant_id=$1 AND id=$2`,
		tenantID, first.Redemption.BillingEventID,
	).Scan(&redemptionEventTime); err != nil {
		t.Fatalf("read redemption billing event time: %v", err)
	}

	claim := seedVoucherMoneyClaim(t, ctx, pool, tenantID, userID, apiKeyID)
	if _, err := reserveVoucherMoney(ctx, pool, tenantID, userID, claim.id, decimal.NewFromInt(100)); err != nil {
		t.Fatalf("reserve redeemed balance before tied reconciliation: %v", err)
	}
	if _, err := billing.NewSettler(pool).Settle(ctx, settleVoucherMoneyRequest(claim, decimal.NewFromInt(100))); err != nil {
		t.Fatalf("settle redeemed balance before tied reconciliation: %v", err)
	}
	var reconciliationEventID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_events (
	tenant_id, claim_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, audit_request_id, occurred_at
) VALUES ($1, $2, 'reconciliation_appended', 0, -50, 2, 0, $3, $4, $5)
RETURNING id`,
		tenantID, claim.id, claim.fingerprint, "money-bridge-recon-tie-refund", redemptionEventTime,
	).Scan(&reconciliationEventID); err != nil {
		t.Fatalf("append tied reconciliation event: %v", err)
	}
	if reconciliationEventID <= first.Redemption.BillingEventID {
		t.Fatalf("reconciliation event id=%d want after voucher event id=%d", reconciliationEventID, first.Redemption.BillingEventID)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE user_balances SET balance=balance+50, version=version+1 WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("credit tied reconciliation balance: %v", err)
	}
	assertVoucherUserBalance(t, ctx, pool, tenantID, userID, "after tied refund", decimal.NewFromInt(50), decimal.Zero)

	replay, err := svc.Redeem(ctx, RedeemInput{
		TenantID:       tenantID,
		UserID:         userID,
		Code:           code,
		IdempotencyKey: "money-bridge-recon-tie",
		SourceIP:       "203.0.113.16",
		RequestID:      "req-money-bridge-recon-tie-replay",
		Now:            now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("replay after tied reconciliation: %v", err)
	}
	if !replay.Idempotent {
		t.Fatalf("replay Idempotent=false, want true")
	}
	if replay.BalanceCents != 5000 {
		t.Fatalf("replay balance cents=%d want current wallet 5000", replay.BalanceCents)
	}
}

type voucherMoneyClaim struct {
	id                int64
	tenantID          int64
	userID            int64
	apiKeyID          int64
	poolGroupID       int64
	providerAccountID int64
	acquisitionToken  uuid.UUID
	fingerprint       string
}

func seedVoucherMoneyVoucher(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, code string, now time.Time) {
	t.Helper()
	seedVoucherMoneyVoucherCurrency(t, ctx, pool, tenantID, code, "USD", now)
}

func seedVoucherMoneyVoucherCurrency(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, code, currency string, now time.Time) {
	t.Helper()
	seedVoucherMoneyVoucherCurrencyAmount(t, ctx, pool, tenantID, code, currency, 10000, now)
}

func seedVoucherMoneyVoucherCurrencyAmount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, code, currency string, amountCents int64, now time.Time) {
	t.Helper()
	hash, fingerprint := CodeHash(tenantID, NormalizeCode(code))
	if _, err := pool.Exec(ctx,
		`INSERT INTO voucher (tenant_id, code_hash, code_fingerprint, amount_cents, currency_code, valid_from, valid_until, max_redemptions, single_use_per_user, status, created_by_admin_id, revoked_reason, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, true, 'active', 1, '', $8, $8)`,
		tenantID, hash, fingerprint, amountCents, currency, now.Add(-time.Minute), now.Add(time.Hour), now,
	); err != nil {
		t.Fatalf("seed voucher: %v", err)
	}
}

func seedLegacyVoucherRedemption(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, code, key string, now time.Time) int64 {
	t.Helper()
	return seedLegacyVoucherRedemptionCurrencyAmount(t, ctx, pool, tenantID, userID, code, key, "USD", 10000, now)
}

func seedLegacyVoucherRedemptionWithoutBillingEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, code, key string, now time.Time) int64 {
	t.Helper()
	hash, _ := CodeHash(tenantID, NormalizeCode(code))
	var voucherID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM voucher WHERE tenant_id=$1 AND code_hash=$2`,
		tenantID, hash,
	).Scan(&voucherID); err != nil {
		t.Fatalf("read legacy voucher id: %v", err)
	}
	var redemptionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO voucher_redemption (
	tenant_id, voucher_id, user_id, idempotency_key, amount_cents, currency_code,
	single_use_per_user, source_ip_hash, request_id, redeemed_at
) VALUES ($1, $2, $3, $4, 10000, 'USD', true, '', 'legacy-redemption-no-event', $5)
RETURNING id`,
		tenantID, voucherID, userID, key, now.Add(time.Second),
	).Scan(&redemptionID); err != nil {
		t.Fatalf("seed legacy redemption without billing event: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE voucher SET redeemed_count=1, status='exhausted' WHERE tenant_id=$1 AND id=$2`,
		tenantID, voucherID,
	); err != nil {
		t.Fatalf("mark no-event legacy voucher redeemed: %v", err)
	}
	return redemptionID
}

func seedLegacyVoucherRedemptionCurrencyAmount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, code, key, currency string, amountCents int64, now time.Time) int64 {
	t.Helper()
	hash, _ := CodeHash(tenantID, NormalizeCode(code))
	var voucherID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM voucher WHERE tenant_id=$1 AND code_hash=$2`,
		tenantID, hash,
	).Scan(&voucherID); err != nil {
		t.Fatalf("read legacy voucher id: %v", err)
	}
	var redemptionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO voucher_redemption (
	tenant_id, voucher_id, user_id, idempotency_key, amount_cents, currency_code,
	single_use_per_user, source_ip_hash, request_id, redeemed_at
) VALUES ($1, $2, $3, $4, $5, $6, true, '', 'legacy-redemption', $7)
RETURNING id`,
		tenantID, voucherID, userID, key, amountCents, currency, now.Add(time.Second),
	).Scan(&redemptionID); err != nil {
		t.Fatalf("seed legacy redemption: %v", err)
	}
	amount := decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
	var billingID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_events (
	tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, voucher_redemption_id
) VALUES ($1, 'voucher_redeemed', $2, $2, 2, 0, 'legacy-voucher', $3)
RETURNING id`,
		tenantID, amount, redemptionID,
	).Scan(&billingID); err != nil {
		t.Fatalf("seed legacy billing event: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE voucher_redemption SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		tenantID, redemptionID, billingID,
	); err != nil {
		t.Fatalf("link legacy billing event: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE voucher SET redeemed_count=1, status='exhausted' WHERE tenant_id=$1 AND id=$2`,
		tenantID, voucherID,
	); err != nil {
		t.Fatalf("mark legacy voucher redeemed: %v", err)
	}
	return redemptionID
}

func seedVoucherMoneyUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID, apiKeyID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("tenant-voucher-money-%d", time.Now().UTC().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "voucher money user",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, 'voucher-money-key', 'hash', 'hk_test_vmoney', 'active') RETURNING id`,
		tenantID, userID,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return tenantID, userID, apiKeyID
}

func seedVoucherMoneyClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID, apiKeyID int64) voucherMoneyClaim {
	t.Helper()
	unique := fmt.Sprintf("voucher-money-%d", time.Now().UTC().UnixNano())
	claim := voucherMoneyClaim{
		tenantID:         tenantID,
		userID:           userID,
		apiKeyID:         apiKeyID,
		acquisitionToken: uuid.New(),
		fingerprint:      "voucher-money-fp-" + unique,
	}
	var providerID, channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pool-"+unique,
	).Scan(&claim.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, claim.poolGroupID, "channel-"+unique,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, cap_concurrency, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 2, 1) RETURNING id`,
		tenantID, providerID, channelID, "account-"+unique,
	).Scan(&claim.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4', $7,
			'1.0', 'standard', $8, $9,
			1, 100, 'USD', NOW() + interval '90 seconds'
		)
		 RETURNING id`,
		tenantID, "idem-"+unique, claim.fingerprint, apiKeyID, userID,
		"lr-"+unique, claim.poolGroupID, claim.providerAccountID, claim.acquisitionToken,
	).Scan(&claim.id); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		tenantID, claim.providerAccountID, claim.acquisitionToken, claim.id,
	); err != nil {
		t.Fatalf("seed pool slot acquisition: %v", err)
	}
	return claim
}

func reserveVoucherMoney(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, claimID int64, cost decimal.Decimal) (billing.Snapshot, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return billing.Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snap, err := billing.Reserve(ctx, tx, billing.ReserveParams{
		TenantID: tenantID,
		UserID:   userID,
		ClaimID:  claimID,
		Cost:     cost,
	})
	if err != nil {
		return billing.Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return billing.Snapshot{}, err
	}
	return snap, nil
}

func assertVoucherUserBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, phase string, wantBalance, wantHeld decimal.Decimal) {
	t.Helper()
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("%s: read user balance: %v", phase, err)
	}
	if !balance.Equal(wantBalance) || !held.Equal(wantHeld) {
		t.Fatalf("%s: balance=%s held=%s want %s/%s", phase, balance, held, wantBalance, wantHeld)
	}
}

func cleanupVoucherMoneyTenant(t *testing.T, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM balance_holds WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `UPDATE voucher_redemption SET billing_event_id=NULL WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM voucher_redemption WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
}

func settleVoucherMoneyRequest(claim voucherMoneyClaim, actualCost decimal.Decimal) billing.SettleRequest {
	return billing.SettleRequest{
		ClaimID:           claim.id,
		AccountID:         claim.providerAccountID,
		AcquisitionToken:  claim.acquisitionToken,
		ActualCost:        actualCost,
		TenantID:          claim.tenantID,
		APIKeyID:          claim.apiKeyID,
		UserID:            claim.userID,
		ProviderAccountID: claim.providerAccountID,
		AttemptSeq:        1,
		RequestedModel:    "gpt-4",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "gpt-4",
		Stream:            false,
		Fingerprint:       claim.fingerprint,
		SnapshotVersion:   "registry:voucher-money;router:test",
		Draft: gateway.UsageRecordDraft{
			TokensInput:           10,
			TokensOutput:          20,
			ActualCost:            actualCost,
			RoutingReason:         []byte(`{"route":"voucher-money"}`),
			EndClass:              gateway.StreamEndClass("non_streaming"),
			UsageSource:           gateway.UsageSourceReported,
			PendingReconciliation: false,
		},
	}
}
