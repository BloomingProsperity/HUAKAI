package quota

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// TestQuotaReconciliationWorkerLogRound 判别 logRound 三分支(聚合每轮一条,不逐租户)。
// §14 变异契约:忙轮分支删掉/翻条件 → busy 子测红;error 字段丢失 → error 子测红;
// 空转改打 Info → idle 子测红。
func TestQuotaReconciliationWorkerLogRound(t *testing.T) {
	newWorker := func() (*ReconciliationWorker, *collectHandler) {
		h := &collectHandler{}
		w := NewReconciliationWorker(nil, 0)
		w.logger = slog.New(h)
		return w, h
	}
	t.Run("忙轮恰好一条Info带processed", func(t *testing.T) {
		w, h := newWorker()
		w.logRound(context.Background(), 7, nil)
		recs := h.snapshot()
		if len(recs) != 1 {
			t.Fatalf("忙轮记录数=%d,want 恰好 1 条汇总(不逐租户逐条打)", len(recs))
		}
		r := recs[0]
		if r.level != slog.LevelInfo || r.attrs["processed"] != "7" || r.attrs["component"] != quotaReconciliationComponent {
			t.Fatalf("忙轮记录=%+v,want Info processed=7 component=%s", r, quotaReconciliationComponent)
		}
	})
	t.Run("错误轮Warn带error", func(t *testing.T) {
		w, h := newWorker()
		w.logRound(context.Background(), 0, errors.New("sweep exploded"))
		recs := h.snapshot()
		if len(recs) != 1 || recs[0].level != slog.LevelWarn || recs[0].attrs["error"] != "sweep exploded" {
			t.Fatalf("错误轮记录=%+v,want 恰好 1 条 Warn error=sweep exploded", recs)
		}
	})
	t.Run("部分失败轮恰好一条Warn不再补Info", func(t *testing.T) {
		// 单租户失败不阻断其余租户,reconciler 常态返回 replayed>0 且 err≠nil;双发会让
		// processed 双计、按 msg=failed 的停摆告警被干扰。变异契约:Info 分支去掉互斥 → 红。
		w, h := newWorker()
		w.logRound(context.Background(), 7, errors.New("tenant 42 poisoned"))
		recs := h.snapshot()
		if len(recs) != 1 {
			t.Fatalf("部分失败轮记录数=%d,want 恰好 1 条(双发=processed 双计)", len(recs))
		}
		r := recs[0]
		if r.level != slog.LevelWarn || r.attrs["processed"] != "7" || r.attrs["error"] != "tenant 42 poisoned" {
			t.Fatalf("部分失败轮记录=%+v,want Warn processed=7 error=tenant 42 poisoned", r)
		}
	})
	t.Run("空转轮零Info", func(t *testing.T) {
		w, h := newWorker()
		w.logRound(context.Background(), 0, nil)
		for _, r := range h.snapshot() {
			if r.level >= slog.LevelInfo {
				t.Fatalf("空转轮出现 Info+ 记录:%+v(分钟级周期会刷屏)", r)
			}
		}
	})
}

// failingSweepStore 让全局 sweep 的首个 store 调用报错,驱动 RunOnce 返回 err 的真实错误轮。
type failingSweepStore struct {
	PGStore
	calls *atomic.Int32
}

func (s failingSweepStore) ListTenantsWithDueReconciliationJobs(context.Context, time.Time, int) ([]int64, error) {
	s.calls.Add(1)
	return nil, errors.New("pg unavailable")
}

// startQuotaWorkerAndWaitRounds 起 worker 跑到 calls 至少 minCalls 后停,返回全部日志记录。
func startQuotaWorkerAndWaitRounds(t *testing.T, store PGStore, calls *atomic.Int32, minCalls int32) []collectRecord {
	t.Helper()
	h := &collectHandler{}
	w := NewReconciliationWorker(NewReconciler(nil, store, ReconcilerOptions{}), 5*time.Millisecond)
	w.logger = slog.New(h)
	w.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < minCalls && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop() // Stop 等协程真退,之后 snapshot 稳定
	if calls.Load() < minCalls {
		t.Fatalf("loop 未驱动足够轮次:calls=%d want>=%d", calls.Load(), minCalls)
	}
	return h.snapshot()
}

// TestQuotaReconciliationWorkerLoopLogsRounds 真 Start→loop 驱动错误轮与空转轮。
// §14 变异契约:loop 改回 `_, _ = w.RunOnce(...)` 丢弃 → 错误轮无 Warn → 红。
func TestQuotaReconciliationWorkerLoopLogsRounds(t *testing.T) {
	t.Run("错误轮Warn带error", func(t *testing.T) {
		var calls atomic.Int32
		recs := startQuotaWorkerAndWaitRounds(t, failingSweepStore{calls: &calls}, &calls, 2)
		warned := false
		for _, r := range recs {
			if r.level == slog.LevelWarn && r.attrs["component"] == quotaReconciliationComponent && r.attrs["error"] != "" {
				warned = true
			}
		}
		if !warned {
			t.Fatal("loop 未把 RunOnce 错误打成 Warn(错误仍被丢弃)")
		}
	})
	t.Run("空转轮零Info且生命周期各一条", func(t *testing.T) {
		var calls atomic.Int32
		recs := startQuotaWorkerAndWaitRounds(t, countingSweepStore{calls: &calls}, &calls, 2)
		var started, stopped bool
		for _, r := range recs {
			switch {
			case r.level == slog.LevelInfo && r.msg == "quota reconciliation worker started":
				started = true
			case r.level == slog.LevelInfo && r.msg == "quota reconciliation worker stopped":
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
