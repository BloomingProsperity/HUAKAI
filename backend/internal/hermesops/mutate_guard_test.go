package hermesops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// This file exercises the S2 orchestrator guards: the concurrency semaphore
// (acquired BEFORE BeginTx) and the tx deadline (client ctx + own-tx UNCERTAIN
// classification). The handler-side per-token rate limiter lives in
// internal/hermeshttp. Fakes (fakeBeginner / fakeMutateTx / txRecorder / errRow)
// are reused from mutate_tx_test.go in this package.

// statementTimeoutTx wraps the package fake tx and records whether a
// `SET LOCAL statement_timeout` Exec was issued (so a legacy/disabled test can
// prove it was NOT) and the millis value (so the deadline test can prove the cap
// equals the tx deadline).
type statementTimeoutTx struct {
	*fakeMutateTx
	setStatementTimeout bool
	statementMillis     int64
}

func (tx *statementTimeoutTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if sql == "SET LOCAL statement_timeout = $1" {
		tx.setStatementTimeout = true
		if len(args) > 0 {
			switch v := args[0].(type) {
			case int64:
				tx.statementMillis = v
			case int:
				tx.statementMillis = int64(v)
			}
		}
		return pgconn.NewCommandTag("SET"), nil
	}
	return tx.fakeMutateTx.Exec(ctx, sql, args...)
}

// statementTimeoutBeginner hands out a statementTimeoutTx so a single test can
// inspect the SET LOCAL behavior of the one tx it opened.
type statementTimeoutBeginner struct {
	rec *txRecorder
	tx  *statementTimeoutTx
}

func (b *statementTimeoutBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.tx = &statementTimeoutTx{fakeMutateTx: &fakeMutateTx{rec: b.rec}}
	return b.tx, nil
}

// --- Test 1: semaphore caps concurrency below N -----------------------------

func TestS2_SemaphoreCapsConcurrencyBelowN(t *testing.T) {
	// Regression (S2 a): with a concurrency cap of N, at most N mutations may reach
	// BeginTx at once; the extras get ErrMutateBusy after the bounded acquire wait
	// rather than all piling onto the pool. The mutate() callback blocks on a
	// release channel so the first N hold their slot while the extras try to enter.
	//
	// Mutation check (run + RED confirmed, then restored): remove the
	// sem.Acquire-before-BeginTx call in Execute and ALL N+2 reach BeginTx (no
	// ErrMutateBusy), so the `busy == 2` / `beginsWhileBlocked <= N` guards go RED.
	const N = 3
	const extra = 2
	// Each concurrent Execute gets its OWN tx recorder (countingBeginner mints a
	// fresh one per BeginTx) so the only shared state is the atomic begin counter —
	// no fixture data race under -race.
	beginner := &countingBeginner{}
	sem := mutateguard.NewSemaphore(N)
	o := NewMutateOrchestrator(beginner,
		WithConcurrencyGuard(sem, 200*time.Millisecond))

	release := make(chan struct{})
	entered := make(chan struct{}, N+extra)

	var busy int64
	var wg sync.WaitGroup
	for i := 0; i < N+extra; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
				entered <- struct{}{}
				<-release // hold the slot until the test releases everyone
				return ToolResult{}, nil
			})
			if errors.Is(err, ErrMutateBusy) {
				atomic.AddInt64(&busy, 1)
			}
		}()
	}

	// Wait until N callbacks are inside mutate() (holding their slot). The extras
	// cannot acquire, so they must time out with ErrMutateBusy.
	for i := 0; i < N; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d/%d mutations entered before timeout", i, N)
		}
	}
	// The extras should fail busy within ~acquireWait; give them margin.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt64(&busy) < extra {
		select {
		case <-deadline:
			t.Fatalf("extras did not return ErrMutateBusy: busy=%d want %d", atomic.LoadInt64(&busy), extra)
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	wg.Wait()

	if got := atomic.LoadInt64(&busy); got != extra {
		t.Fatalf("busy=%d want %d (N+%d concurrent, cap=%d)", got, extra, extra, N)
	}
	if begins := atomic.LoadInt64(&beginner.begins); begins > N {
		// Only the N admitted mutations may reach BeginTx while the extras are
		// blocked busy. If the semaphore were not acquired before BeginTx, all N+2
		// would have begun.
		t.Fatalf("beginCount=%d exceeded the concurrency cap %d — sem not acquired before BeginTx", begins, N)
	}
}

// countingBeginner counts BeginTx calls atomically (the package fakeBeginner is
// not concurrency-safe). Each call returns a fake tx over its OWN fresh recorder
// so concurrent Executes never share mutable fixture state (race-clean).
type countingBeginner struct {
	begins int64
}

func (b *countingBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	atomic.AddInt64(&b.begins, 1)
	return &fakeMutateTx{rec: &txRecorder{}}, nil
}

// --- Test 2: acquire-timeout is a clean busy, not a hang --------------------

func TestS2_AcquireTimeoutIsCleanBusyNotHang(t *testing.T) {
	// Regression (S2 a): N=1, the slot held by a blocked mutation, a SECOND Execute
	// returns ErrMutateBusy within ~acquireWait — a clean back-pressure signal, not
	// a hang. The whole test must finish well under its own deadline.
	//
	// Mutation check (run + RED confirmed, then restored): change Semaphore.Acquire
	// to use context.Background() (no acquireWait bound) and the 2nd Execute blocks
	// forever — this test's 2s wall-clock guard trips RED.
	rec := &txRecorder{}
	sem := mutateguard.NewSemaphore(1)
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec},
		WithConcurrencyGuard(sem, 100*time.Millisecond))

	release := make(chan struct{})
	holding := make(chan struct{})
	go func() {
		_, _ = o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
			close(holding)
			<-release
			return ToolResult{}, nil
		})
	}()
	<-holding // the single slot is now held

	done := make(chan error, 1)
	go func() {
		_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{}, nil
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrMutateBusy) {
			t.Fatalf("2nd Execute err=%v want ErrMutateBusy", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("2nd Execute hung past acquireWait — busy must be bounded, never a hang")
	}
	close(release)
}

// --- Test 3: tx-deadline aborts a STUCK mutation + releases conn/lock --------

func TestS2_TxDeadlineAbortsStuckMutationAndRollsBack(t *testing.T) {
	// Regression (S2 b): a mutation that runs past the tx deadline is cut — the
	// mutate() sees mutCtx.Done(), Execute surfaces a deadline error, and the defer
	// rolls the tx back (releasing the conn + advisory lock). The mutation does NOT
	// commit.
	//
	// Mutation check (run + RED confirmed, then restored): drop the
	// context.WithTimeout(ctx, txDeadline) in Execute (so mutCtx == ctx, never
	// cancelled); the mutate() below then runs to completion and the tx COMMITS —
	// the `rollbackCount==1` / `commitCount==0` / deadline-error guards go RED.
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec},
		WithTxDeadline(50*time.Millisecond))

	in := baseRecord()
	_, err := o.Execute(context.Background(), "lock", in, func(ctx context.Context, _ pgx.Tx) (ToolResult, error) {
		// Sleep past the deadline; honor cancellation so the abort is observed.
		select {
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
			return ToolResult{Summary: map[string]any{"committed": true}}, nil
		}
	})
	if err == nil {
		t.Fatalf("stuck mutation past tx deadline returned nil err (it committed?)")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want a context.DeadlineExceeded (in-tx deadline)", err)
	}
	if rec.commitCount != 0 {
		t.Fatalf("commitCount=%d want 0 (a timed-out mutation must NOT commit)", rec.commitCount)
	}
	if rec.rollbackCount != 1 {
		t.Fatalf("rollbackCount=%d want 1 (deadline must roll back to release conn + advisory lock)", rec.rollbackCount)
	}
	// The rollback MUST run on an independent (non-cancelled) ctx — "the whole
	// point": if it derived from the dead deadline ctx it would itself be
	// cancelled and the pool connection + advisory lock would leak. MUTATION:
	// thread the dead mutCtx into the rollback defer instead of the independent
	// 5s ctx -> rollbackLiveCtx==0 here -> RED. (review S2 close)
	if rec.rollbackLiveCtx != 1 {
		t.Fatalf("rollbackLiveCtx=%d want 1 (rollback must use an INDEPENDENT live ctx, not the dead deadline ctx, or the conn+lock leak)", rec.rollbackLiveCtx)
	}
}

// --- Test 4: deadline does NOT cut a legit slow replay (90s headroom) --------

func TestS2_DeadlineDoesNotCutLegitSlowReplay(t *testing.T) {
	// Regression (S2 b — the load-bearing 90s headroom): the DEFAULT tx deadline
	// (90s, 3x the 30s dlq_replay inner claim lease) must NOT cut a legitimately
	// slow settlement. A replay that takes ~40s (well under 90s, but longer than a
	// naive 30s = lease deadline would tolerate) COMMITS.
	//
	// This test compresses time: it uses the config's DEFAULT-derived headroom
	// ratio rather than literally sleeping 40s. The deadline is set to 3x a
	// simulated lease (mirroring 90s = 3x30s); the fake replay takes 1.33x the
	// lease (mirroring 40s = 1.33x30s) — under the 3x deadline, so it commits.
	//
	// Mutation check (run + RED confirmed, then restored): lower the deadline to 1x
	// the lease (mirroring "default tightened to 30s"); the 1.33x replay is then cut
	// and the `commitCount==1` guard goes RED — proving the 3x (90s) headroom is
	// load-bearing, not cosmetic.
	const lease = 30 * time.Millisecond  // stands in for the real 30s claim lease
	const deadline = 3 * lease           // stands in for the 90s default (3x lease)
	const replayDuration = lease * 4 / 3 // ~1.33x lease (stands in for a 40s replay)

	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec}, WithTxDeadline(deadline))

	dlqReplayRec := baseRecord()
	dlqReplayRec.ToolName = ToolDLQReplay
	dlqReplayRec.OwnTx = true // dlq_replay is own-tx

	_, err := o.Execute(context.Background(), "lock", dlqReplayRec, func(ctx context.Context, _ pgx.Tx) (ToolResult, error) {
		select {
		case <-ctx.Done():
			return ToolResult{}, ctx.Err() // cut early == defect
		case <-time.After(replayDuration):
			return ToolResult{Summary: map[string]any{"status": "delivered"}}, nil
		}
	})
	if err != nil {
		t.Fatalf("legit slow replay (%v, under %v deadline) was cut: err=%v — 90s headroom not load-bearing", replayDuration, deadline, err)
	}
	if rec.commitCount != 1 {
		t.Fatalf("commitCount=%d want 1 (a legit slow replay under the deadline must commit)", rec.commitCount)
	}
}

// --- Test 5: own-tx timeout classified UNCERTAIN, not falsely rolled-back ----

func TestS2_OwnTxDeadlineClassifiedUncertainNotRolledBack(t *testing.T) {
	// Regression (S2 b CORRECTNESS, DISCRIMINATING): a dlq_replay (OWN-TX) whose
	// inner own-tx ALREADY committed before the tx deadline tripped a later step
	// MUST surface ErrMutateTimeoutUncertain (-> error_class mutate_timeout_uncertain,
	// reconciliation needed), NEVER a clean rolled-back mutation_failed. An IN-TX
	// tool's deadline rolls the mutation back atomically and stays a plain deadline
	// (mutation_failed/mutate_timeout). The danger guarded: falsely telling the
	// operator a replay "did not happen" when it actually persisted.
	//
	// Mutation check (run + RED confirmed, then restored): in classifyMutateErr,
	// remove the `!rec.OwnTx` short-circuit so EVERY deadline error is wrapped (or
	// drop the wrap entirely so none are) — either way own and in-tx agree and the
	// `ownWrapped != inWrapped` self-proving guard goes RED.
	run := func(ownTx bool) error {
		rec := &txRecorder{}
		o := NewMutateOrchestrator(&fakeBeginner{rec: rec}, WithTxDeadline(time.Hour))
		audit := baseRecord()
		audit.OwnTx = ownTx
		if ownTx {
			audit.ToolName = ToolDLQReplay
		}
		// Simulate the inner own-tx having committed, then a later step hitting the
		// deadline: mutate() returns context.DeadlineExceeded directly (the tx
		// deadline is huge here so only this injected error drives the path).
		_, err := o.Execute(context.Background(), "lock", audit, func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{}, context.DeadlineExceeded
		})
		if err == nil {
			t.Fatalf("ownTx=%v: deadline mutate returned nil err", ownTx)
		}
		return err
	}

	ownErr := run(true)
	inErr := run(false)

	ownUncertain := errors.Is(ownErr, ErrMutateTimeoutUncertain)
	inUncertain := errors.Is(inErr, ErrMutateTimeoutUncertain)

	if !ownUncertain {
		t.Fatalf("own-tx deadline did NOT classify UNCERTAIN: %v — a persisted replay would be falsely reported rolled back", ownErr)
	}
	if inUncertain {
		t.Fatalf("in-tx deadline WRONGLY classified UNCERTAIN: %v — in-tx rolls back atomically, must stay mutation_failed", inErr)
	}
	if ownUncertain == inUncertain {
		t.Fatalf("tx-mode did not change the classification (own=%v in=%v) — rec.OwnTx not threaded into the deadline path", ownUncertain, inUncertain)
	}
	// The in-tx deadline must still be a plain deadline error (handler maps it to a
	// clean timeout, not uncertain).
	if !errors.Is(inErr, context.DeadlineExceeded) {
		t.Fatalf("in-tx deadline err=%v want context.DeadlineExceeded", inErr)
	}
}

// --- statement_timeout: enabled sets it; disabled does NOT (legacy) ----------

func TestS2_TxDeadlineSetsStatementTimeoutScopedToTx(t *testing.T) {
	// Regression (S2 b): when the tx deadline is enabled, Execute issues a
	// `SET LOCAL statement_timeout` = the deadline millis on THIS tx (server-side
	// cap, auto-reset at tx end). With the deadline DISABLED (0), it must NOT issue
	// it — proving the legacy path is untouched.
	t.Run("enabled sets statement_timeout = deadline millis", func(t *testing.T) {
		rec := &txRecorder{}
		b := &statementTimeoutBeginner{rec: rec}
		o := NewMutateOrchestrator(b, WithTxDeadline(90*time.Second))
		_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{}, nil
		})
		if err != nil {
			t.Fatalf("execute err=%v want nil", err)
		}
		if b.tx == nil || !b.tx.setStatementTimeout {
			t.Fatalf("SET LOCAL statement_timeout was NOT issued under an enabled deadline")
		}
		if b.tx.statementMillis != (90 * time.Second).Milliseconds() {
			t.Fatalf("statement_timeout=%d ms want %d (== tx deadline)", b.tx.statementMillis, (90 * time.Second).Milliseconds())
		}
	})
	t.Run("disabled does NOT issue statement_timeout (legacy)", func(t *testing.T) {
		rec := &txRecorder{}
		b := &statementTimeoutBeginner{rec: rec}
		o := NewMutateOrchestrator(b) // no deadline option == disabled
		_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{}, nil
		})
		if err != nil {
			t.Fatalf("execute err=%v want nil", err)
		}
		if b.tx != nil && b.tx.setStatementTimeout {
			t.Fatalf("SET LOCAL statement_timeout issued with the deadline DISABLED — legacy path must not be touched")
		}
	})
}
