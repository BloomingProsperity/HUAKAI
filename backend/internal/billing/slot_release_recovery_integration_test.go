//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

func TestSettler_AbortAlreadyReclaimedSlotFinalizesOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedHeldSettlerGraph(t, ctx, pool, "abort-already-reclaimed")
	reclaimSettlerSlot(t, ctx, pool, seed)

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "lease_expired", "req-abort-already-reclaimed", 7, nil)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}

	assertRecoveredSlotFinalization(t, ctx, pool, seed, "aborted", "released", "orphan_swept", decimal.RequireFromString("10.00000000"), 1)
	assertFinalizationRows(t, ctx, pool, seed.claimID, "claim_aborted", decimal.Zero)
}

func TestSettler_SettleAlreadyReclaimedSlotCommitsMoneyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedHeldSettlerGraph(t, ctx, pool, "settle-already-reclaimed")
	reclaimSettlerSlot(t, ctx, pool, seed)
	actualCost := decimal.RequireFromString("0.03000000")

	res, err := NewSettler(pool).Settle(ctx, settleRequest(seed, actualCost))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res == nil || !res.NewUserBalance.Equal(decimal.RequireFromString("9.97000000")) {
		t.Fatalf("Settle result=%+v，want balance 9.97000000", res)
	}
	var claimCost decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT actual_cost FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&claimCost); err != nil {
		t.Fatalf("read claim cost: %v", err)
	}
	if !claimCost.Equal(actualCost) {
		t.Fatalf("claim actual_cost=%s，want %s", claimCost, actualCost)
	}

	assertRecoveredSlotFinalization(t, ctx, pool, seed, "committed", "captured", "orphan_swept", decimal.RequireFromString("9.97000000"), 1)
	assertFinalizationRows(t, ctx, pool, seed.claimID, "claim_committed", actualCost)
}

func TestSettler_MissingSlotStillReturnsErrSlotReleaseMissed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedHeldSettlerGraph(t, ctx, pool, "abort-missing-slot")
	if _, err := pool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE acquisition_token=$1`, seed.acquisitionToken); err != nil {
		t.Fatalf("delete slot: %v", err)
	}

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "missing_slot", "req-abort-missing-slot", 0, nil)
	if !errors.Is(err, ErrSlotReleaseMissed) {
		t.Fatalf("Abort err=%v，want %v", err, ErrSlotReleaseMissed)
	}

	assertRecoveredSlotFinalization(t, ctx, pool, seed, "reserving", "held", "", decimal.RequireFromString("10.00000000"), 2)
	assertFinalizationRows(t, ctx, pool, seed.claimID, "claim_aborted", decimal.Zero, 0)
}

func seedHeldSettlerGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) settlerSeed {
	t.Helper()
	seed := seedSettlerGraph(t, ctx, pool, suffix)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held)
		 VALUES ($1, $2, 10, 0)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}
	return seed
}

func reclaimSettlerSlot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed settlerSeed) {
	t.Helper()
	tag, err := pool.Exec(ctx,
		`WITH reclaimed AS (
		    UPDATE pool_slot_acquisitions
		    SET status='orphan_swept', released_at=NOW(), release_reason='released_lease_expired'
		    WHERE acquisition_token=$1 AND status='acquired'
		    RETURNING provider_account_id
		 )
		 UPDATE provider_accounts pa
		 SET in_flight_count=GREATEST(pa.in_flight_count-1, 0), updated_at=NOW()
		 FROM reclaimed r
		 WHERE pa.id=r.provider_account_id`,
		seed.acquisitionToken,
	)
	if err != nil {
		t.Fatalf("reclaim slot: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("reclaimed accounts=%d，want 1", tag.RowsAffected())
	}
}

func assertRecoveredSlotFinalization(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	seed settlerSeed,
	wantClaimStatus string,
	wantHoldState string,
	wantSlotStatus string,
	wantBalance decimal.Decimal,
	wantInFlight int,
) {
	t.Helper()
	var claimStatus, holdState, slotStatus string
	var balance, held decimal.Decimal
	var inFlight int
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&claimStatus); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM balance_holds WHERE claim_id=$1`, seed.claimID).Scan(&holdState); err != nil {
		t.Fatalf("read hold: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight: %v", err)
	}
	err := pool.QueryRow(ctx, `SELECT status FROM pool_slot_acquisitions WHERE acquisition_token=$1`, seed.acquisitionToken).Scan(&slotStatus)
	if wantSlotStatus == "" {
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("missing slot err=%v，want pgx.ErrNoRows", err)
		}
	} else if err != nil {
		t.Fatalf("read slot: %v", err)
	} else if slotStatus != wantSlotStatus {
		t.Fatalf("slot status=%q，want %q", slotStatus, wantSlotStatus)
	}
	if claimStatus != wantClaimStatus || holdState != wantHoldState {
		t.Fatalf("claim/hold=%q/%q，want %q/%q", claimStatus, holdState, wantClaimStatus, wantHoldState)
	}
	wantHeld := decimal.Zero
	if wantHoldState == "held" {
		wantHeld = decimal.RequireFromString("0.01000000")
	}
	if !balance.Equal(wantBalance) || !held.Equal(wantHeld) {
		t.Fatalf("balance/held=%s/%s，want balance=%s hold_state=%s", balance, held, wantBalance, wantHoldState)
	}
	if inFlight != wantInFlight {
		t.Fatalf("in_flight_count=%d，want %d", inFlight, wantInFlight)
	}
}

func assertFinalizationRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64, eventType string, actualCost decimal.Decimal, optionalWant ...int) {
	t.Helper()
	want := 1
	if len(optionalWant) > 0 {
		want = optionalWant[0]
	}
	var events, matchingEvents, usages, matchingUsages int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE event_type=$2 AND actual_cost=$3)
		 FROM billing_events WHERE claim_id=$1`,
		claimID, eventType, actualCost,
	).Scan(&events, &matchingEvents); err != nil {
		t.Fatalf("count billing events: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE actual_cost=$2)
		 FROM usage_records WHERE claim_id=$1`,
		claimID, actualCost,
	).Scan(&usages, &matchingUsages); err != nil {
		t.Fatalf("count usage records: %v", err)
	}
	if events != want || matchingEvents != want || usages != want || matchingUsages != want {
		t.Fatalf("events/matching_events/usages/matching_usages=%d/%d/%d/%d，want %d", events, matchingEvents, usages, matchingUsages, want)
	}
}
