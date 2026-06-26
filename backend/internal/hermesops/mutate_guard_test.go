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

// 本文件演练 S2 orchestrator 的 guard:并发 semaphore(在 BeginTx BEFORE(之前)获取)与 tx
// 截止(客户端 ctx + own-tx 的 UNCERTAIN 分类)。handler 侧的每 token 限流器位于
// internal/hermeshttp。各 fake(fakeBeginner / fakeMutateTx / txRecorder / errRow)复用自本包
// 的 mutate_tx_test.go。

// statementTimeoutTx 包裹本包的 fake tx,记录是否发出过 `SET LOCAL statement_timeout` 的 Exec
// (这样 legacy/disabled 测试就能证明它 NOT(没有)发出)以及 millis 值(这样截止测试就能证明该上限
// 等于 tx 截止)。
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

// statementTimeoutBeginner 交出一个 statementTimeoutTx,这样单个测试就能检视它所开的那一个 tx
// 的 SET LOCAL 行为。
type statementTimeoutBeginner struct {
	rec *txRecorder
	tx  *statementTimeoutTx
}

func (b *statementTimeoutBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.tx = &statementTimeoutTx{fakeMutateTx: &fakeMutateTx{rec: b.rec}}
	return b.tx, nil
}

// --- 测试 1:semaphore 把并发上限压在 N 之下 -----------------------------

func TestS2_SemaphoreCapsConcurrencyBelowN(t *testing.T) {
	// 回归(S2 a):并发上限为 N 时,同时最多有 N 个 mutation 能到达 BeginTx;多出来的那些会在有界的
	// 获取等待之后拿到 ErrMutateBusy,而非全部压向连接池。mutate() 回调阻塞在一个 release 通道上,
	// 这样前 N 个就在持有各自槽位的同时,让多余的去尝试进入。
	//
	// 变异检查(已运行 + 确认变红,随后还原):移除 Execute 中 sem.Acquire-before-BeginTx 的调用,
	// 全部 N+2 都会到达 BeginTx(没有 ErrMutateBusy),于是 `busy == 2` / `beginsWhileBlocked <= N`
	// 的断言变红。
	const N = 3
	const extra = 2
	// 每个并发的 Execute 都拿到自己 OWN(独立)的 tx recorder(countingBeginner 每次 BeginTx 新铸
	// 一个),这样唯一的共享状态就是那个原子的 begin 计数器——在 -race 下无 fixture 数据竞争。
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
				<-release // 持有该槽位,直到测试放行所有人
				return ToolResult{}, nil
			})
			if errors.Is(err, ErrMutateBusy) {
				atomic.AddInt64(&busy, 1)
			}
		}()
	}

	// 等到有 N 个回调进入 mutate() 内部(持有各自的槽位)。多出来的那些无法获取,所以它们必须以
	// ErrMutateBusy 超时。
	for i := 0; i < N; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d/%d mutations entered before timeout", i, N)
		}
	}
	// 多出来的那些应在 ~acquireWait 内以 busy 失败;给它们留余量。
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
		// 当多出来的那些被 busy 阻塞时,只有 N 个被准入的 mutation 可以到达 BeginTx。如果 semaphore
		// 没在 BeginTx 之前获取,那 N+2 个就都会开始。
		t.Fatalf("beginCount=%d exceeded the concurrency cap %d — sem not acquired before BeginTx", begins, N)
	}
}

// countingBeginner 原子地计数 BeginTx 调用(本包的 fakeBeginner 不是并发安全的)。每次调用返回
// 一个基于它自己 OWN(全新)recorder 的 fake tx,这样并发的 Execute 就永不共享可变的 fixture 状态
// (race 干净)。
type countingBeginner struct {
	begins int64
}

func (b *countingBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	atomic.AddInt64(&b.begins, 1)
	return &fakeMutateTx{rec: &txRecorder{}}, nil
}

// --- 测试 2:获取超时是干净的 busy,而非挂起 --------------------

func TestS2_AcquireTimeoutIsCleanBusyNotHang(t *testing.T) {
	// 回归(S2 a):N=1,槽位被一个阻塞的 mutation 持有,第二个(SECOND)Execute 在 ~acquireWait 内
	// 返回 ErrMutateBusy——一个干净的背压信号,而非挂起。整个测试必须远在自身截止之内完成。
	//
	// 变异检查(已运行 + 确认变红,随后还原):把 Semaphore.Acquire 改为使用 context.Background()
	// (无 acquireWait 界),第二个 Execute 就会永远阻塞——本测试 2s 的墙钟保护会触发变红。
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
	<-holding // 这唯一的槽位现在已被持有

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

// --- 测试 3:tx 截止中止一个 STUCK(卡住)的 mutation + 释放 conn/lock --------

func TestS2_TxDeadlineAbortsStuckMutationAndRollsBack(t *testing.T) {
	// 回归(S2 b):一个运行超过 tx 截止的 mutation 被切断——mutate() 看到 mutCtx.Done(),Execute
	// 呈现一个截止 error,defer 把 tx 回滚(释放 conn + advisory lock)。该 mutation NOT(不)提交。
	//
	// 变异检查(已运行 + 确认变红,随后还原):去掉 Execute 中的 context.WithTimeout(ctx, txDeadline)
	// (这样 mutCtx == ctx,永不被取消);下面的 mutate() 就会跑完,tx COMMITS(提交)——于是
	// `rollbackCount==1` / `commitCount==0` / 截止 error 的断言变红。
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec},
		WithTxDeadline(50*time.Millisecond))

	in := baseRecord()
	_, err := o.Execute(context.Background(), "lock", in, func(ctx context.Context, _ pgx.Tx) (ToolResult, error) {
		// 睡过截止;尊重取消,这样中止才被观测到。
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
	// 回滚 MUST(必须)在一个独立的(未被取消的)ctx 上运行——这才是"关键所在":如果它从那个
	// 已死的截止 ctx 派生,它自己就会被取消,连接池连接 + advisory lock 就会泄漏。变异:把已死的
	// mutCtx 穿进回滚 defer,而非那个独立的 5s ctx -> 这里 rollbackLiveCtx==0 -> 变红。(review S2 收口)
	if rec.rollbackLiveCtx != 1 {
		t.Fatalf("rollbackLiveCtx=%d want 1 (rollback must use an INDEPENDENT live ctx, not the dead deadline ctx, or the conn+lock leak)", rec.rollbackLiveCtx)
	}
}

// --- 测试 4:截止 NOT(不会)切断一个合法的慢 replay(90s 余量)--------

func TestS2_DeadlineDoesNotCutLegitSlowReplay(t *testing.T) {
	// 回归(S2 b——承重的 90s 余量):DEFAULT(默认)的 tx 截止(90s,是 30s dlq_replay 内层 claim
	// lease 的 3 倍)必须 NOT(不)切断一个合法的慢结算。一个耗时约 40s 的 replay(远在 90s 之内,但
	// 比一个朴素的 30s = lease 截止所能容忍的更长)会 COMMITS(提交)。
	//
	// 本测试压缩了时间:它用 config 由 DEFAULT 推导出的余量比例,而非真的睡 40s。截止被设为一个模拟
	// lease 的 3 倍(镜像 90s = 3x30s);fake replay 耗时 1.33 倍 lease(镜像 40s = 1.33x30s)——
	// 在 3 倍截止之内,所以它提交。
	//
	// 变异检查(已运行 + 确认变红,随后还原):把截止降到 1 倍 lease(镜像"默认被收紧到 30s");
	// 那么 1.33 倍的 replay 就会被切断,`commitCount==1` 的断言变红——证明 3 倍(90s)余量是承重的,
	// 而非装饰。
	const lease = 30 * time.Millisecond  // 代表真实的 30s claim lease
	const deadline = 3 * lease           // 代表 90s 默认(3 倍 lease)
	const replayDuration = lease * 4 / 3 // ~1.33 倍 lease(代表一个 40s 的 replay)

	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec}, WithTxDeadline(deadline))

	dlqReplayRec := baseRecord()
	dlqReplayRec.ToolName = ToolDLQReplay
	dlqReplayRec.OwnTx = true // dlq_replay 是 own-tx

	_, err := o.Execute(context.Background(), "lock", dlqReplayRec, func(ctx context.Context, _ pgx.Tx) (ToolResult, error) {
		select {
		case <-ctx.Done():
			return ToolResult{}, ctx.Err() // 过早切断 == 缺陷
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

// --- 测试 5:own-tx 超时被分类为 UNCERTAIN,而非被错误地报告为已回滚 ----

func TestS2_OwnTxDeadlineClassifiedUncertainNotRolledBack(t *testing.T) {
	// 回归(S2 b 正确性、有区分度):一个 dlq_replay(OWN-TX),其内层 own-tx 在 tx 截止触发某个后续
	// 步骤之前 ALREADY(已经)提交,MUST(必须)呈现 ErrMutateTimeoutUncertain(-> error_class
	// mutate_timeout_uncertain,需要对账),NEVER(绝不)呈现干净的、已回滚的 mutation_failed。一个
	// IN-TX 工具的截止会把 mutation 原子地回滚,仍是一个普通的截止(mutation_failed/mutate_timeout)。
	// 它防范的危险:在 replay 实际上已持久化时,错误地告诉运营者它"没有发生"。
	//
	// 变异检查(已运行 + 确认变红,随后还原):在 classifyMutateErr 中移除 `!rec.OwnTx` 短路,这样
	// EVERY(每一个)截止 error 都被包裹(或彻底去掉包裹,这样一个都不被包裹)——无论哪种,own 与 in-tx
	// 都会一致,`ownWrapped != inWrapped` 这个自证断言就会变红。
	run := func(ownTx bool) error {
		rec := &txRecorder{}
		o := NewMutateOrchestrator(&fakeBeginner{rec: rec}, WithTxDeadline(time.Hour))
		audit := baseRecord()
		audit.OwnTx = ownTx
		if ownTx {
			audit.ToolName = ToolDLQReplay
		}
		// 模拟内层 own-tx 已提交、随后某个后续步骤触发截止:mutate() 直接返回
		// context.DeadlineExceeded(这里 tx 截止设得很大,所以只有这个注入的 error 驱动该路径)。
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
	// in-tx 截止仍必须是一个普通的截止 error(handler 把它映射到一个干净的 timeout,而非 uncertain)。
	if !errors.Is(inErr, context.DeadlineExceeded) {
		t.Fatalf("in-tx deadline err=%v want context.DeadlineExceeded", inErr)
	}
}

// --- statement_timeout:启用时设置它;禁用时 NOT(不)设置(legacy)----------

func TestS2_TxDeadlineSetsStatementTimeoutScopedToTx(t *testing.T) {
	// 回归(S2 b):当 tx 截止被启用时,Execute 在 THIS(本)tx 上发出一个
	// `SET LOCAL statement_timeout` = 截止 millis(服务端上限,tx 结束时自动复位)。当截止被
	// DISABLED(禁用,为 0)时,它必须 NOT(不)发出它——证明 legacy 路径未被触动。
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
		o := NewMutateOrchestrator(b) // 不带截止选项 == 禁用
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
