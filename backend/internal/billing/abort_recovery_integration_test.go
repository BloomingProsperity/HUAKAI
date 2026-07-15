//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// TestSettlerAbort_ATCD3002_SeventhTransactionCommits 精确越过旧六次预算，
// 并验证只有最终提交的 Tx2 留下一份钱账证据。
func TestSettlerAbort_ATCD3002_SeventhTransactionCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-seventh-success")
	fault := installAbortConflictSequenceFault(t, ctx, pool, seed.claimID, 6)

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "cd3_seventh_success", "req-cd3-002", 7, nil)
	if err != nil {
		t.Fatalf("Abort 第七笔 Tx2 应成功：%v", err)
	}
	if got := fault.attempts(t, ctx); got != 7 {
		t.Fatalf("Tx2 次数=%d，want 7", got)
	}
	assertAbortedEvidenceOnce(t, ctx, pool, seed, "cd3_seventh_success", 7)
	assertAbortHoldReleased(t, ctx, pool, seed)
}

// TestSettlerAbort_ATCD3003_ExhaustionExpeditesLeaseAndPreservesState 固定
// 九次冲突后的 fail-closed 状态：主错误保留、钱账未动，仅 lease 提前到期。
func TestSettlerAbort_ATCD3003_ExhaustionExpeditesLeaseAndPreservesState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-exhaust-expedite")
	fault := installAbortConflictSequenceFault(t, ctx, pool, seed.claimID, 9)

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "cd3_exhausted", "req-cd3-003", 0, nil)
	assertPGConflictCode(t, err, "40001")
	if got := fault.attempts(t, ctx); got != 9 {
		t.Fatalf("Tx2 次数=%d，want 9", got)
	}
	assertAbortFrozenState(t, ctx, pool, seed, true)
}

// TestSettlerAbort_ATCD3004_ExpeditedClaimSweepsInOneRound 证明提前到期只提供
// 恢复资格，最终状态与钱账副作用仍由原 LeaseSweeper 的真实 Abort Tx2 决定。
func TestSettlerAbort_ATCD3004_ExpeditedClaimSweepsInOneRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	drainBillingLeaseSweeperBacklog(t, ctx, pool)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-sweep-expedited")
	fault := installAbortConflictSequenceFault(t, ctx, pool, seed.claimID, 9)

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "cd3_exhausted", "req-cd3-004", 0, nil)
	assertPGConflictCode(t, err, "40001")
	if got := fault.attempts(t, ctx); got != 9 {
		t.Fatalf("Tx2 次数=%d，want 9", got)
	}
	assertAbortFrozenState(t, ctx, pool, seed, true)
	fault.drop(t)

	processed, sweepErr := NewLeaseSweeper(pool, NewSettler(pool), 100).SweepOnce(ctx)
	if sweepErr != nil {
		t.Fatalf("SweepOnce：%v", sweepErr)
	}
	if processed != 1 {
		t.Fatalf("SweepOnce processed=%d，want 1", processed)
	}
	assertAbortedEvidenceOnce(t, ctx, pool, seed, "lease_expired", 0)
	assertAbortHoldReleased(t, ctx, pool, seed)
}

// TestSettlerAbort_ATCD3005_ExpediteFailureKeepsPrimaryConflict 让恢复 UPDATE
// 自身失败，验证 lease 维持未来值、观测递增，返回值仍是原 Tx2 冲突。
func TestSettlerAbort_ATCD3005_ExpediteFailureKeepsPrimaryConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-expedite-failure")
	conflictFault := installAbortConflictSequenceFault(t, ctx, pool, seed.claimID, 9)
	expediteFault := installAbortLeaseExpediteFailure(t, ctx, pool, seed.claimID)
	metricBefore := billingAbortMetricValue("40001", "expedite_failed")

	err := NewSettler(pool).Abort(ctx, seed.tenantID, seed.claimID, "cd3_expedite_failed", "req-cd3-005", 0, nil)
	assertPGConflictCode(t, err, "40001")
	if got := conflictFault.attempts(t, ctx); got != 9 {
		t.Fatalf("Tx2 次数=%d，want 9", got)
	}
	if got := expediteFault.attempts(t, ctx); got != 1 {
		t.Fatalf("lease 加速尝试=%d，want 1", got)
	}
	if got := billingAbortMetricValue("40001", "expedite_failed") - metricBefore; got != 1 {
		t.Fatalf("expedite_failed metric delta=%d，want 1", got)
	}
	assertAbortFrozenState(t, ctx, pool, seed, false)
}

// TestSettlerAbort_ATCD3006_PendingDeliveryExcludesSweeper 固定既有候选保护：
// 即使 lease 已到期，未决交付后结算仍禁止零成本 Abort。
func TestSettlerAbort_ATCD3006_PendingDeliveryExcludesSweeper(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	drainBillingLeaseSweeperBacklog(t, ctx, pool)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-pending-delivery")
	seedPendingSettlementRecovery(t, ctx, pool, seed.tenantID, seed.claimID)
	if _, err := pool.Exec(ctx,
		`UPDATE billing_ledger_claims SET lease_expires_at=NOW()-interval '1 second' WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.claimID,
	); err != nil {
		t.Fatalf("expire protected claim：%v", err)
	}

	processed, err := NewLeaseSweeper(pool, NewSettler(pool), 100).SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce：%v", err)
	}
	if processed != 0 {
		t.Fatalf("protected SweepOnce processed=%d，want 0", processed)
	}
	assertAbortFrozenState(t, ctx, pool, seed, true)
}

// TestSettlerAbort_ATCD3007_DualSweepersRaceLateAbortExactlyOnce 让两个 sweeper
// 与迟到的真实 Abort 同时争同一 claim，最终只能有一份终态、释放与审计证据。
func TestSettlerAbort_ATCD3007_DualSweepersRaceLateAbortExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	drainBillingLeaseSweeperBacklog(t, ctx, pool)
	seed := seedAbortRecoveryGraph(t, ctx, pool, "cd3-dual-sweeper-race")
	if _, err := pool.Exec(ctx,
		`UPDATE billing_ledger_claims SET lease_expires_at=NOW()-interval '1 second' WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.claimID,
	); err != nil {
		t.Fatalf("expire racing claim：%v", err)
	}

	type raceResult struct {
		kind      string
		processed int
		err       error
	}
	start := make(chan struct{})
	results := make(chan raceResult, 3)
	settler := NewSettler(pool)
	sweepers := []*LeaseSweeper{
		NewLeaseSweeper(pool, settler, 100),
		NewLeaseSweeper(pool, settler, 100),
	}
	var wg sync.WaitGroup
	for i, sweeper := range sweepers {
		wg.Add(1)
		go func(index int, current *LeaseSweeper) {
			defer wg.Done()
			<-start
			n, err := current.SweepOnce(ctx)
			results <- raceResult{kind: fmt.Sprintf("sweeper-%d", index+1), processed: n, err: err}
		}(i, sweeper)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		err := settler.Abort(ctx, seed.tenantID, seed.claimID, "late_real_abort", "req-cd3-007", 0, nil)
		results <- raceResult{kind: "late-abort", err: err}
	}()
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for result := range results {
		switch result.kind {
		case "late-abort":
			if result.err == nil {
				successes++
			} else if !errors.Is(result.err, ErrClaimNotReserving) {
				t.Fatalf("late Abort err=%v", result.err)
			}
		default:
			if result.err != nil {
				t.Fatalf("%s err=%v", result.kind, result.err)
			}
			successes += result.processed
		}
	}
	if successes != 1 {
		t.Fatalf("成功终结次数=%d，want 1", successes)
	}
	assertTerminalAbortEvidenceExactlyOnce(t, ctx, pool, seed)
}

func seedAbortRecoveryGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) settlerSeed {
	t.Helper()
	seed := seedSettlerGraph(t, ctx, pool, label)
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held)
		 VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed user balance：%v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold：%v", err)
	}
	return seed
}

func assertPGConflictCode(t *testing.T, err error, want string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("err=%T %v，want SQLSTATE %s", err, err, want)
	}
}

func assertAbortFrozenState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed settlerSeed, leaseExpired bool) {
	t.Helper()
	var status, holdState, slotStatus string
	var lease, dbNow time.Time
	var held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT c.status, c.lease_expires_at, h.state, b.held, s.status, clock_timestamp()
		 FROM billing_ledger_claims c
		 JOIN balance_holds h ON h.claim_id=c.id
		 JOIN user_balances b ON b.tenant_id=c.tenant_id AND b.user_id=c.user_id
		 JOIN pool_slot_acquisitions s ON s.tenant_id=c.tenant_id AND s.claim_id=c.id
		 WHERE c.tenant_id=$1 AND c.id=$2`,
		seed.tenantID, seed.claimID,
	).Scan(&status, &lease, &holdState, &held, &slotStatus, &dbNow); err != nil {
		t.Fatalf("read frozen abort state：%v", err)
	}
	if status != "reserving" || holdState != "held" || slotStatus != "acquired" || !held.Equal(decimal.RequireFromString("0.01000000")) {
		t.Fatalf("status/hold/slot/held=%s/%s/%s/%s，want reserving/held/acquired/0.01000000", status, holdState, slotStatus, held)
	}
	if leaseExpired && lease.After(dbNow) {
		t.Fatalf("lease=%s，want <= DB now %s", lease, dbNow)
	}
	if !leaseExpired && !lease.After(dbNow) {
		t.Fatalf("lease=%s，want > DB now %s after expedite failure", lease, dbNow)
	}
	var events, usage int
	if err := pool.QueryRow(ctx,
		`SELECT
		 (SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted'),
		 (SELECT count(*) FROM usage_records WHERE claim_id=$1)`,
		seed.claimID,
	).Scan(&events, &usage); err != nil {
		t.Fatalf("count frozen evidence：%v", err)
	}
	if events != 0 || usage != 0 {
		t.Fatalf("events/usage=%d/%d，want 0/0 before real terminal Tx2", events, usage)
	}
}

func assertAbortHoldReleased(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed settlerSeed) {
	t.Helper()
	var holdState string
	var held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT h.state, b.held
		 FROM balance_holds h
		 JOIN user_balances b ON b.tenant_id=h.tenant_id AND b.user_id=h.user_id
		 WHERE h.claim_id=$1`,
		seed.claimID,
	).Scan(&holdState, &held); err != nil {
		t.Fatalf("read released hold：%v", err)
	}
	if holdState != "released" || !held.IsZero() {
		t.Fatalf("hold state/held=%s/%s，want released/0", holdState, held)
	}
}

func assertTerminalAbortEvidenceExactlyOnce(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed settlerSeed) {
	t.Helper()
	var status, reason string
	if err := pool.QueryRow(ctx,
		`SELECT status, aborted_reason FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.claimID,
	).Scan(&status, &reason); err != nil {
		t.Fatalf("read terminal claim：%v", err)
	}
	if status != "aborted" || (reason != "lease_expired" && reason != "late_real_abort") {
		t.Fatalf("status/reason=%s/%s，want aborted and one racing reason", status, reason)
	}
	var events, usage, releasedSlots int
	if err := pool.QueryRow(ctx,
		`SELECT
		 (SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted'),
		 (SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost=0),
		 (SELECT count(*) FROM pool_slot_acquisitions WHERE claim_id=$1 AND status='released_failure')`,
		seed.claimID,
	).Scan(&events, &usage, &releasedSlots); err != nil {
		t.Fatalf("count terminal evidence：%v", err)
	}
	if events != 1 || usage != 1 || releasedSlots != 1 {
		t.Fatalf("events/usage/released slots=%d/%d/%d，want 1/1/1", events, usage, releasedSlots)
	}
	assertAbortHoldReleased(t, ctx, pool, seed)
}

func drainBillingLeaseSweeperBacklog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	sweeper := NewLeaseSweeper(pool, NewSettler(pool), 100)
	for round := 0; round < 10; round++ {
		processed, err := sweeper.SweepOnce(ctx)
		if err != nil {
			t.Fatalf("drain billing backlog round %d：%v", round+1, err)
		}
		if processed == 0 {
			return
		}
	}
	t.Fatal("drain billing backlog：10 轮后仍有共享库积压")
}

type abortConflictSequenceFault struct {
	pool        *pgxpool.Pool
	sequenceRef string
	sequenceID  string
	functionID  string
	triggerID   string
	dropOnce    sync.Once
}

func installAbortConflictSequenceFault(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64, failures int) *abortConflictSequenceFault {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	sequenceName := "huakai_cd3_abort_seq_" + suffix
	functionName := "huakai_cd3_abort_fail_" + suffix
	triggerName := "huakai_cd3_abort_fail_" + suffix
	fault := &abortConflictSequenceFault{
		pool:        pool,
		sequenceRef: "public." + sequenceName,
		sequenceID:  pgx.Identifier{"public", sequenceName}.Sanitize(),
		functionID:  pgx.Identifier{"public", functionName}.Sanitize(),
		triggerID:   pgx.Identifier{triggerName}.Sanitize(),
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, fault.sequenceID)); err != nil {
		t.Fatalf("create abort conflict sequence：%v", err)
	}
	createFunction := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.claim_id = %d
		AND NEW.event_type = 'claim_aborted'
		AND nextval('%s'::regclass) <= %d THEN
		RAISE EXCEPTION 'forced abort Tx2 serialization conflict' USING ERRCODE = '40001';
	END IF;
	RETURN NEW;
END;
$$`, fault.functionID, claimID, fault.sequenceRef, failures)
	if _, err := pool.Exec(ctx, createFunction); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, fault.sequenceID))
		t.Fatalf("create abort conflict function：%v", err)
	}
	createTrigger := fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON billing_events
FOR EACH ROW EXECUTE FUNCTION %s()`, fault.triggerID, fault.functionID)
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fault.functionID))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, fault.sequenceID))
		t.Fatalf("create abort conflict trigger：%v", err)
	}
	t.Cleanup(func() { fault.drop(t) })
	return fault
}

func (f *abortConflictSequenceFault) attempts(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var got int64
	if err := f.pool.QueryRow(ctx, fmt.Sprintf(`SELECT last_value FROM %s`, f.sequenceRef)).Scan(&got); err != nil {
		t.Fatalf("read abort conflict attempts：%v", err)
	}
	return got
}

func (f *abortConflictSequenceFault) drop(t *testing.T) {
	t.Helper()
	f.dropOnce.Do(func() {
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_events`, f.triggerID)); err != nil {
			t.Errorf("drop abort conflict trigger：%v", err)
		}
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, f.functionID)); err != nil {
			t.Errorf("drop abort conflict function：%v", err)
		}
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, f.sequenceID)); err != nil {
			t.Errorf("drop abort conflict sequence：%v", err)
		}
	})
}

type abortLeaseExpediteFailure struct {
	pool        *pgxpool.Pool
	sequenceRef string
	sequenceID  string
	functionID  string
	triggerID   string
	dropOnce    sync.Once
}

func installAbortLeaseExpediteFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64) *abortLeaseExpediteFailure {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	sequenceName := "huakai_cd3_expedite_seq_" + suffix
	functionName := "huakai_cd3_expedite_fail_" + suffix
	triggerName := "huakai_cd3_expedite_fail_" + suffix
	fault := &abortLeaseExpediteFailure{
		pool:        pool,
		sequenceRef: "public." + sequenceName,
		sequenceID:  pgx.Identifier{"public", sequenceName}.Sanitize(),
		functionID:  pgx.Identifier{"public", functionName}.Sanitize(),
		triggerID:   pgx.Identifier{triggerName}.Sanitize(),
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, fault.sequenceID)); err != nil {
		t.Fatalf("create expedite failure sequence：%v", err)
	}
	createFunction := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.id = %d AND NEW.lease_expires_at IS DISTINCT FROM OLD.lease_expires_at THEN
		PERFORM nextval('%s'::regclass);
		RAISE EXCEPTION 'forced abort lease expedite failure' USING ERRCODE = '55P03';
	END IF;
	RETURN NEW;
END;
$$`, fault.functionID, claimID, fault.sequenceRef)
	if _, err := pool.Exec(ctx, createFunction); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, fault.sequenceID))
		t.Fatalf("create expedite failure function：%v", err)
	}
	createTrigger := fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE UPDATE OF lease_expires_at ON billing_ledger_claims
FOR EACH ROW EXECUTE FUNCTION %s()`, fault.triggerID, fault.functionID)
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fault.functionID))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, fault.sequenceID))
		t.Fatalf("create expedite failure trigger：%v", err)
	}
	t.Cleanup(func() { fault.drop(t) })
	return fault
}

func (f *abortLeaseExpediteFailure) attempts(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var got int64
	var called bool
	if err := f.pool.QueryRow(ctx, fmt.Sprintf(`SELECT last_value, is_called FROM %s`, f.sequenceRef)).Scan(&got, &called); err != nil {
		t.Fatalf("read expedite failure attempts：%v", err)
	}
	if !called {
		return 0
	}
	return got
}

func (f *abortLeaseExpediteFailure) drop(t *testing.T) {
	t.Helper()
	f.dropOnce.Do(func() {
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_ledger_claims`, f.triggerID)); err != nil {
			t.Errorf("drop expedite failure trigger：%v", err)
		}
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, f.functionID)); err != nil {
			t.Errorf("drop expedite failure function：%v", err)
		}
		if _, err := f.pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, f.sequenceID)); err != nil {
			t.Errorf("drop expedite failure sequence：%v", err)
		}
	})
}
