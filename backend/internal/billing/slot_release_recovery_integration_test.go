//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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

// TestSettler_SlotLeaseReclaimRacesAbortAndSettleExactlyOnce 用两个独立 Serializable
// 事务真实争抢同一槽，证明无论回收还是终结先赢，槽与账号计数都只变化一次。
// 变异判据：删除 acquired 守卫会双减，删除任一整事务重试会泄漏 40001 或缺失终态。
func TestSettler_SlotLeaseReclaimRacesAbortAndSettleExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tracer := &slotFinalizationRaceTracer{}
	settlerPool := openSlotFinalizationRacePool(t, ctx, pool, tracer)
	settler := NewSettler(settlerPool)

	type scenario struct {
		name          string
		operation     slotReleaseOperation
		reclaimerWins bool
	}
	scenarios := []scenario{
		{name: "abort_reclaimer_wins", operation: slotReleaseAbort, reclaimerWins: true},
		{name: "abort_finalizer_wins", operation: slotReleaseAbort, reclaimerWins: false},
		{name: "settle_reclaimer_wins", operation: slotReleaseSettle, reclaimerWins: true},
		{name: "settle_finalizer_wins", operation: slotReleaseSettle, reclaimerWins: false},
	}
	type winCounts struct {
		reclaimer int
		finalizer int
	}
	counts := map[slotReleaseOperation]*winCounts{
		slotReleaseAbort:  {},
		slotReleaseSettle: {},
	}

	for roundIndex := 0; roundIndex < 12; roundIndex++ {
		scenario := scenarios[roundIndex%len(scenarios)]
		t.Run(fmt.Sprintf("%s_round_%02d", scenario.name, roundIndex+1), func(t *testing.T) {
			seed := seedHeldSettlerGraph(t, ctx, pool, fmt.Sprintf("slot-finalize-race-%s-%02d", scenario.name, roundIndex+1))
			raceRound := newSlotFinalizationRaceRound(seed, scenario.reclaimerWins)
			tracer.setRound(raceRound)
			defer tracer.clearRound(raceRound)

			start := make(chan struct{})
			reclaimResults := make(chan slotReclaimRaceResult, 1)
			finalizationResults := make(chan slotFinalizationRaceResult, 1)
			go func() {
				<-start
				reclaimResults <- runSlotReclaimRace(ctx, pool, seed, raceRound)
			}()
			go func() {
				<-start
				result := slotFinalizationRaceResult{}
				switch scenario.operation {
				case slotReleaseAbort:
					result.err = settler.Abort(ctx, seed.tenantID, seed.claimID, "slot_reclaim_race", fmt.Sprintf("req-slot-race-%d", roundIndex+1), 7, nil)
				case slotReleaseSettle:
					result.settle, result.err = settler.Settle(ctx, settleRequest(seed, decimal.RequireFromString("0.03000000")))
				}
				finalizationResults <- result
			}()
			close(start)

			reclaimResult := <-reclaimResults
			finalizationResult := <-finalizationResults
			if reclaimResult.err != nil {
				t.Fatalf("槽回收事务 err=%v", reclaimResult.err)
			}
			if finalizationResult.err != nil {
				if isReserveSerializationConflict(finalizationResult.err) {
					t.Fatalf("Settler 泄漏 Serializable 冲突：%v", finalizationResult.err)
				}
				t.Fatalf("Settler 终结 err=%v", finalizationResult.err)
			}
			if reclaimResult.reclaimed != scenario.reclaimerWins {
				t.Fatalf("回收方最终命中=%v，want %v", reclaimResult.reclaimed, scenario.reclaimerWins)
			}

			finalizerSlotAttempts := int(raceRound.finalizerSlotAttempts.Load())
			finalizerConflicts := int(raceRound.finalizerSerializationFailures.Load())
			if scenario.reclaimerWins {
				counts[scenario.operation].reclaimer++
				if reclaimResult.attempts != 1 || reclaimResult.serializationFailures != 0 {
					t.Fatalf("回收方 attempts/40001=%d/%d，want 1/0", reclaimResult.attempts, reclaimResult.serializationFailures)
				}
				if finalizerSlotAttempts != 2 || finalizerConflicts != 1 {
					t.Fatalf("Settler 槽 UPDATE attempts/40001=%d/%d，want 2/1", finalizerSlotAttempts, finalizerConflicts)
				}
			} else {
				counts[scenario.operation].finalizer++
				if reclaimResult.attempts != 2 || reclaimResult.serializationFailures != 1 {
					t.Fatalf("回收方 attempts/40001=%d/%d，want 2/1", reclaimResult.attempts, reclaimResult.serializationFailures)
				}
				if finalizerSlotAttempts != 1 || finalizerConflicts != 0 {
					t.Fatalf("Settler 槽 UPDATE attempts/40001=%d/%d，want 1/0", finalizerSlotAttempts, finalizerConflicts)
				}
			}

			wantClaimStatus := "aborted"
			wantHoldState := "released"
			wantSlotStatus := "released_failure"
			wantBalance := decimal.RequireFromString("10.00000000")
			wantEventType := "claim_aborted"
			wantActualCost := decimal.Zero
			if scenario.operation == slotReleaseSettle {
				wantClaimStatus = "committed"
				wantHoldState = "captured"
				wantSlotStatus = "released_success"
				wantBalance = decimal.RequireFromString("9.97000000")
				wantEventType = "claim_committed"
				wantActualCost = decimal.RequireFromString("0.03000000")
				if finalizationResult.settle == nil || !finalizationResult.settle.NewUserBalance.Equal(wantBalance) {
					t.Fatalf("Settle result=%+v，want balance %s", finalizationResult.settle, wantBalance)
				}
			}
			if scenario.reclaimerWins {
				wantSlotStatus = "orphan_swept"
			}
			assertRecoveredSlotFinalization(t, ctx, pool, seed, wantClaimStatus, wantHoldState, wantSlotStatus, wantBalance, 1)
			assertFinalizationRows(t, ctx, pool, seed.claimID, wantEventType, wantActualCost)
		})
	}

	for operation, got := range counts {
		if got.reclaimer == 0 || got.finalizer == 0 {
			t.Fatalf("%s 胜序命中 回收/终结=%d/%d，want 两种均出现", operation, got.reclaimer, got.finalizer)
		}
		t.Logf("%s 胜序命中 回收/终结=%d/%d", operation, got.reclaimer, got.finalizer)
	}
}

type slotFinalizationRaceResult struct {
	settle *SettleResult
	err    error
}

type slotReclaimRaceResult struct {
	reclaimed             bool
	attempts              int
	serializationFailures int
	err                   error
}

type slotFinalizationRaceRound struct {
	claimID                        int64
	acquisitionToken               uuid.UUID
	reclaimerWins                  bool
	reclaimerReady                 chan struct{}
	finalizerSlotStarted           chan struct{}
	finalizerSlotFinished          chan struct{}
	finalizerSlotStartedOnce       sync.Once
	finalizerSlotFinishedOnce      sync.Once
	finalizerSlotAttempts          atomic.Int32
	finalizerSerializationFailures atomic.Int32
}

func newSlotFinalizationRaceRound(seed settlerSeed, reclaimerWins bool) *slotFinalizationRaceRound {
	return &slotFinalizationRaceRound{
		claimID:               seed.claimID,
		acquisitionToken:      seed.acquisitionToken,
		reclaimerWins:         reclaimerWins,
		reclaimerReady:        make(chan struct{}),
		finalizerSlotStarted:  make(chan struct{}),
		finalizerSlotFinished: make(chan struct{}),
	}
}

type slotFinalizationRaceTracer struct {
	mu    sync.RWMutex
	round *slotFinalizationRaceRound
}

type slotFinalizationTraceKey struct{}

func (t *slotFinalizationRaceTracer) setRound(round *slotFinalizationRaceRound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.round = round
}

func (t *slotFinalizationRaceTracer) clearRound(round *slotFinalizationRaceRound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.round == round {
		t.round = nil
	}
}

func (t *slotFinalizationRaceTracer) currentRound() *slotFinalizationRaceRound {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.round
}

func (t *slotFinalizationRaceTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	round := t.currentRound()
	if round == nil {
		return ctx
	}
	if isClaimLockRaceQuery(data, round.claimID) {
		select {
		case <-round.reclaimerReady:
		case <-ctx.Done():
		}
	}
	if !isSlotReleaseRaceQuery(data, round.acquisitionToken) {
		return ctx
	}
	round.finalizerSlotAttempts.Add(1)
	round.finalizerSlotStartedOnce.Do(func() { close(round.finalizerSlotStarted) })
	return context.WithValue(ctx, slotFinalizationTraceKey{}, round)
}

func (t *slotFinalizationRaceTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	round, _ := ctx.Value(slotFinalizationTraceKey{}).(*slotFinalizationRaceRound)
	if round == nil {
		return
	}
	if data.Err != nil {
		if billingRetrySQLState(data.Err) == "40001" {
			round.finalizerSerializationFailures.Add(1)
		}
		return
	}
	round.finalizerSlotFinishedOnce.Do(func() { close(round.finalizerSlotFinished) })
}

func isClaimLockRaceQuery(data pgx.TraceQueryStartData, claimID int64) bool {
	if !strings.Contains(data.SQL, "FROM billing_ledger_claims") || !strings.Contains(data.SQL, "FOR UPDATE") || len(data.Args) == 0 {
		return false
	}
	got, ok := raceInt64Arg(data.Args[0])
	return ok && got == claimID
}

func isSlotReleaseRaceQuery(data pgx.TraceQueryStartData, token uuid.UUID) bool {
	if !strings.Contains(data.SQL, "WITH released AS") || !strings.Contains(data.SQL, "UPDATE pool_slot_acquisitions") {
		return false
	}
	// sqlc 会按参数首次出现位置排列实参；查询字段调整后 UUID 不保证在首位。
	for _, arg := range data.Args {
		got, ok := arg.(uuid.UUID)
		if ok && got == token {
			return true
		}
	}
	return false
}

func raceInt64Arg(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int32:
		return int64(value), true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}

func openSlotFinalizationRacePool(t *testing.T, ctx context.Context, source *pgxpool.Pool, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	config := source.Config()
	config.ConnConfig.Tracer = tracer
	config.MinConns = 0
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("打开带追踪的 Settler 连接池：%v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("探测带追踪的 Settler 连接池：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func runSlotReclaimRace(ctx context.Context, pool *pgxpool.Pool, seed settlerSeed, round *slotFinalizationRaceRound) slotReclaimRaceResult {
	result := slotReclaimRaceResult{}
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(round.reclaimerReady) }) }
	defer signalReady()

	for attempt := 1; attempt <= 4; attempt++ {
		result.attempts = attempt
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			result.err = err
			return result
		}

		if attempt == 1 && !round.reclaimerWins {
			var status string
			if err = tx.QueryRow(ctx, `SELECT status FROM pool_slot_acquisitions WHERE acquisition_token=$1`, seed.acquisitionToken).Scan(&status); err == nil && status != "acquired" {
				err = fmt.Errorf("槽初始 status=%q，want acquired", status)
			}
			if err != nil {
				_ = tx.Rollback(ctx)
				result.err = err
				return result
			}
			signalReady()
			if err = waitSlotRaceSignal(ctx, round.finalizerSlotFinished, "等待 Settler 更新槽"); err != nil {
				_ = tx.Rollback(ctx)
				result.err = err
				return result
			}
		}

		tag, execErr := tx.Exec(ctx, reclaimSettlerSlotSQL, seed.acquisitionToken)
		if attempt == 1 && round.reclaimerWins && execErr == nil {
			if tag.RowsAffected() != 1 {
				execErr = fmt.Errorf("回收账号行数=%d，want 1", tag.RowsAffected())
			} else {
				signalReady()
				execErr = waitSlotRaceSignal(ctx, round.finalizerSlotStarted, "等待 Settler 争抢槽")
			}
		}
		if execErr == nil {
			execErr = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if execErr == nil {
			result.reclaimed = tag.RowsAffected() == 1
			return result
		}
		_ = tx.Rollback(ctx)
		if billingRetrySQLState(execErr) == "40001" {
			result.serializationFailures++
			continue
		}
		result.err = execErr
		return result
	}
	result.err = fmt.Errorf("槽回收 Serializable 重试耗尽")
	return result
}

func waitSlotRaceSignal(ctx context.Context, signal <-chan struct{}, operation string) error {
	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", operation, ctx.Err())
	}
}

const reclaimSettlerSlotSQL = `WITH reclaimed AS (
    UPDATE pool_slot_acquisitions
    SET status='orphan_swept', released_at=NOW(), release_reason='released_lease_expired'
    WHERE acquisition_token=$1 AND status='acquired'
    RETURNING provider_account_id
 )
 UPDATE provider_accounts pa
 SET in_flight_count=GREATEST(pa.in_flight_count-1, 0), updated_at=NOW()
 FROM reclaimed r
 WHERE pa.id=r.provider_account_id`

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
	tag, err := pool.Exec(ctx, reclaimSettlerSlotSQL, seed.acquisitionToken)
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
