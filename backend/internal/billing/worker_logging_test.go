package billing

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// collectRecord 是收集到的一条日志(级别/消息/扁平化字段),供断言用。
type collectRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// collectHandler 收集所有级别的 slog 记录(含 Debug),用于断言 worker 日志行为。
type collectHandler struct {
	mu   sync.Mutex
	recs []collectRecord
}

func (h *collectHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *collectHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]string, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, collectRecord{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (h *collectHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *collectHandler) WithGroup(string) slog.Handler      { return h }

// snapshot 返回记录副本(loop 协程并发写,断言前拷贝)。
func (h *collectHandler) snapshot() []collectRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]collectRecord, len(h.recs))
	copy(out, h.recs)
	return out
}

// TestLeaseSweeperLogRound 判别 logRound 三分支。§14 变异契约:忙轮分支删掉/翻条件 →
// busy 子测红;error 字段丢失 → error 子测红;空转改打 Info → idle 子测红。
func TestLeaseSweeperLogRound(t *testing.T) {
	newSweeper := func() (*LeaseSweeper, *collectHandler) {
		h := &collectHandler{}
		s := NewLeaseSweeper(nil, nil, 0)
		s.logger = slog.New(h)
		return s, h
	}
	t.Run("忙轮恰好一条Info带processed", func(t *testing.T) {
		s, h := newSweeper()
		s.logRound(context.Background(), 5, nil)
		recs := h.snapshot()
		if len(recs) != 1 {
			t.Fatalf("忙轮记录数=%d,want 恰好 1 条汇总", len(recs))
		}
		r := recs[0]
		if r.level != slog.LevelInfo || r.attrs["processed"] != "5" || r.attrs["component"] != leaseSweeperComponent {
			t.Fatalf("忙轮记录=%+v,want Info processed=5 component=%s", r, leaseSweeperComponent)
		}
	})
	t.Run("错误轮Warn带error", func(t *testing.T) {
		s, h := newSweeper()
		s.logRound(context.Background(), 0, errors.New("db down"))
		recs := h.snapshot()
		if len(recs) != 1 || recs[0].level != slog.LevelWarn || recs[0].attrs["error"] != "db down" {
			t.Fatalf("错误轮记录=%+v,want 恰好 1 条 Warn error=db down", recs)
		}
	})
	t.Run("部分失败轮恰好一条Warn不再补Info", func(t *testing.T) {
		// sweepOnce 逐 claim 容错,降级期常态返回 swept>0 且 err≠nil;双发 Warn+Info 会让
		// 按 processed 求和的日志派生指标双计回收量。变异契约:Info 分支去掉 err 互斥 → 红。
		s, h := newSweeper()
		s.logRound(context.Background(), 5, errors.New("partial abort"))
		recs := h.snapshot()
		if len(recs) != 1 {
			t.Fatalf("部分失败轮记录数=%d,want 恰好 1 条(双发=processed 双计)", len(recs))
		}
		r := recs[0]
		if r.level != slog.LevelWarn || r.attrs["processed"] != "5" || r.attrs["error"] != "partial abort" {
			t.Fatalf("部分失败轮记录=%+v,want Warn processed=5 error=partial abort", r)
		}
	})
	t.Run("空转轮零Info", func(t *testing.T) {
		s, h := newSweeper()
		s.logRound(context.Background(), 0, nil)
		for _, r := range h.snapshot() {
			if r.level >= slog.LevelInfo {
				t.Fatalf("空转轮出现 Info+ 记录:%+v(30s 周期会刷屏)", r)
			}
		}
	})
}

// nonAbortingSettler 只为满足 Start 的非 nil 校验;查询先失败故 Abort 不会被调,嵌接口即可。
type nonAbortingSettler struct{ Settler }

// TestLeaseSweeperLoopLogsFailedRound 用真 Start→loop 驱动:pool 指向不可达地址 → sweepOnce
// 出错必须被 loop 打成 Warn。§14 变异契约:loop 改回 `_, _ = s.sweepOnce(ctx)` 丢弃 → 无 Warn → 红。
func TestLeaseSweeperLoopLogsFailedRound(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody:nothing@127.0.0.1:1/absent?connect_timeout=1")
	if err != nil {
		t.Fatalf("构造惰性 pool: %v", err)
	}
	defer pool.Close()
	h := &collectHandler{}
	s := NewLeaseSweeper(pool, nonAbortingSettler{}, 0)
	s.interval = 5 * time.Millisecond
	s.logger = slog.New(h)
	s.Start(context.Background())

	// 15s 死线:路径依赖真实网络栈(连不可达端口),CI 冷 race 构建+满载下 5s 曾抓到过 1 次闪红。
	deadline := time.Now().Add(15 * time.Second)
	warned := false
	for !warned && time.Now().Before(deadline) {
		for _, r := range h.snapshot() {
			if r.level == slog.LevelWarn && r.attrs["component"] == leaseSweeperComponent && r.attrs["error"] != "" {
				warned = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	if !warned {
		t.Fatal("loop 未把 sweepOnce 错误打成 Warn(处理结果仍被丢弃)")
	}
	var started, stopped bool
	for _, r := range h.snapshot() {
		if r.level == slog.LevelInfo && r.msg == "lease sweeper started" {
			started = true
		}
		if r.level == slog.LevelInfo && r.msg == "lease sweeper stopped" {
			stopped = true
		}
	}
	if !started || !stopped {
		t.Fatalf("生命周期日志缺失:started=%v stopped=%v", started, stopped)
	}
}

// staticFinalizer 每次调用返回固定 (finalized, err) 并原子计数,供 loop 级测试。
type staticFinalizer struct {
	calls     atomic.Int32
	finalized int
	err       error
}

func (f *staticFinalizer) FinalizePendingNoUsage(context.Context, time.Time, int32, string, time.Time) (int, error) {
	f.calls.Add(1)
	return f.finalized, f.err
}

// startPendingWorkerAndWaitRounds 起 worker 跑到至少 minCalls 轮后停,返回全部日志记录。
func startPendingWorkerAndWaitRounds(t *testing.T, f *staticFinalizer, minCalls int32) []collectRecord {
	t.Helper()
	h := &collectHandler{}
	w := NewPendingReconciliationWorker(f, 5*time.Millisecond, 0, 0)
	w.logger = slog.New(h)
	w.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for f.calls.Load() < minCalls && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop() // Stop 等协程真退,之后 snapshot 稳定
	if f.calls.Load() < minCalls {
		t.Fatalf("loop 未驱动足够轮次:calls=%d want>=%d", f.calls.Load(), minCalls)
	}
	return h.snapshot()
}

// TestPendingReconciliationWorkerLoopLogsRounds 真 Start→loop 驱动三种轮次形态。
// §14 变异契约:loop 改回 `_, _ = w.RunOnce(...)` → 忙轮无 Info/错误轮无 Warn → 红;
// logRound 忙轮打两条 → roundInfos>calls → 红;空转打 Info → idle 子测红。
func TestPendingReconciliationWorkerLoopLogsRounds(t *testing.T) {
	t.Run("忙轮每轮一条Info且processed正确", func(t *testing.T) {
		f := &staticFinalizer{finalized: 3}
		recs := startPendingWorkerAndWaitRounds(t, f, 2)
		calls := f.calls.Load()
		roundInfos := 0
		for _, r := range recs {
			if r.level == slog.LevelWarn {
				t.Fatalf("无错误却出现 Warn:%+v", r)
			}
			if r.level == slog.LevelInfo && r.attrs["processed"] != "" {
				roundInfos++
				if r.attrs["processed"] != "3" || r.attrs["component"] != pendingReconciliationComponent {
					t.Fatalf("忙轮汇总字段错:%+v,want processed=3 component=%s", r, pendingReconciliationComponent)
				}
			}
		}
		if roundInfos < 1 || roundInfos > int(calls) {
			t.Fatalf("忙轮汇总条数=%d calls=%d,want 每轮恰好一条(1<=n<=calls)", roundInfos, calls)
		}
	})
	t.Run("错误轮Warn带error", func(t *testing.T) {
		f := &staticFinalizer{err: errors.New("finalize blew up")}
		recs := startPendingWorkerAndWaitRounds(t, f, 2)
		warned := false
		for _, r := range recs {
			if r.level == slog.LevelInfo && r.attrs["processed"] != "" {
				t.Fatalf("错误轮不应出现处理量 Info:%+v", r)
			}
			if r.level == slog.LevelWarn && r.attrs["error"] == "finalize blew up" && r.attrs["component"] == pendingReconciliationComponent {
				warned = true
			}
		}
		if !warned {
			t.Fatal("loop 未把 RunOnce 错误打成 Warn(错误仍被丢弃)")
		}
	})
	t.Run("空转轮零Info且生命周期各一条", func(t *testing.T) {
		f := &staticFinalizer{}
		recs := startPendingWorkerAndWaitRounds(t, f, 2)
		// started/stopped 存在性断言(审查 S3:此前只豁免不断言,删掉生命周期日志测试仍绿)。
		var started, stopped bool
		for _, r := range recs {
			switch {
			case r.level == slog.LevelInfo && r.msg == "pending reconciliation worker started":
				started = true
			case r.level == slog.LevelInfo && r.msg == "pending reconciliation worker stopped":
				stopped = true
			case r.level >= slog.LevelInfo:
				t.Fatalf("空转轮出现非生命周期 Info+ 记录:%+v(会刷屏)", r)
			}
		}
		if !started || !stopped {
			t.Fatalf("生命周期日志缺失:started=%v stopped=%v", started, stopped)
		}
	})
}
