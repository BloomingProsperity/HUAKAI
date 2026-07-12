package logsink

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type fakeStore struct {
	mu      sync.Mutex
	batches [][]Entry
	err     error
}

func (f *fakeStore) InsertRuntimeLogs(_ context.Context, entries []Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeStore) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件未在时限内满足")
}

// 级别闸门:非 warn/error 直接丢弃不入队(变异:放行 info → 红)。
func TestEnqueueLevelGate(t *testing.T) {
	s := New(WithQueueSize(4))
	s.Enqueue(Entry{Level: "info", Message: "x"})
	s.Enqueue(Entry{Level: "debug", Message: "x"})
	if len(s.queue) != 0 {
		t.Fatalf("非 warn+ 不应入队: %d", len(s.queue))
	}
	s.Enqueue(Entry{Level: "warn", Message: "x"})
	s.Enqueue(Entry{Level: "error", Message: "x"})
	if len(s.queue) != 2 {
		t.Fatalf("warn/error 应入队: %d", len(s.queue))
	}
}

// 队列满非阻塞丢弃并计数(变异:改成阻塞发送或漏计数 → 红/卡死)。
func TestEnqueueOverflowDrops(t *testing.T) {
	s := New(WithQueueSize(2))
	for i := 0; i < 5; i++ {
		s.Enqueue(Entry{Level: "warn", Message: "x"})
	}
	_, _, dropped, _ := s.Health()
	if dropped != 3 {
		t.Fatalf("超载丢弃计数 = %d, 期望 3", dropped)
	}
}

// 批量落库:达到批量阈值即刷,store 收到全部条目;入库计数与最后刷新时刻更新。
func TestFlushBatches(t *testing.T) {
	store := &fakeStore{}
	s := New(WithQueueSize(64), WithBatch(3, 20*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, store)
	for i := 0; i < 7; i++ {
		s.Enqueue(Entry{Level: "warn", Message: "m"})
	}
	waitFor(t, func() bool { return store.total() == 7 })
	_, inserted, dropped, last := s.Health()
	if inserted != 7 || dropped != 0 {
		t.Fatalf("inserted=%d dropped=%d", inserted, dropped)
	}
	if last.IsZero() {
		t.Fatal("lastFlush 未更新")
	}
}

// store 报错 → 整批计入丢弃,不重试不 panic(变异:删丢弃计数 → 红)。
func TestFlushErrorCountsDropped(t *testing.T) {
	store := &fakeStore{err: errors.New("db down")}
	s := New(WithQueueSize(64), WithBatch(2, 20*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, store)
	for i := 0; i < 4; i++ {
		s.Enqueue(Entry{Level: "error", Message: "m"})
	}
	waitFor(t, func() bool {
		_, _, dropped, _ := s.Health()
		return dropped == 4
	})
}

// 停机 drain:ctx 取消后队列存量仍被刷出。
func TestDrainOnShutdown(t *testing.T) {
	store := &fakeStore{}
	s := New(WithQueueSize(64), WithBatch(100, time.Hour)) // 大批量+长间隔,只有 drain 能刷出
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx, store)
	for i := 0; i < 5; i++ {
		s.Enqueue(Entry{Level: "warn", Message: "m"})
	}
	cancel()
	waitFor(t, func() bool { return store.total() == 5 })
}

// SlogTap:warn+ 才采集;component/request_id attr 提升为字段,其余进 Attrs。
func TestSlogTapCapture(t *testing.T) {
	s := New(WithQueueSize(8))
	tap := SlogTap(s)
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "skip me", 0)
	tap(context.Background(), rec)
	if len(s.queue) != 0 {
		t.Fatal("info 不应采集")
	}
	rec = slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)
	rec.AddAttrs(slog.String("component", "billing"), slog.String("request_id", "req-1"), slog.Int("n", 3))
	tap(context.Background(), rec)
	if len(s.queue) != 1 {
		t.Fatal("error 应采集")
	}
	e := <-s.queue
	if e.Level != "error" || e.Component != "billing" || e.RequestID != "req-1" {
		t.Fatalf("字段提取错: %+v", e)
	}
	if v, ok := e.Attrs["n"]; !ok || v != int64(3) {
		t.Fatalf("普通 attr 未保留: %#v", e.Attrs)
	}
}

// zap Core 包装:warn+ 旁路采集且主输出照旧;info 不采集;禁写值被替换。
func TestZapCoreCapture(t *testing.T) {
	s := New(WithQueueSize(8))
	observed, logs := newObservedZap(t, s)
	observed.Info("quiet", zap.String("k", "v"))
	if len(s.queue) != 0 {
		t.Fatal("info 不应采集")
	}
	observed.Warn("careful", zap.String("request_id", "req-9"), zap.String("k", "v"))
	if len(s.queue) != 1 {
		t.Fatal("warn 应采集")
	}
	e := <-s.queue
	if e.Level != "warn" || e.RequestID != "req-9" || e.Attrs["k"] != "v" {
		t.Fatalf("zap 采集错: %+v", e)
	}
	// 主输出不受旁路影响(变异:Write 不再委托内层 → 红)。
	if logs() != 2 {
		t.Fatalf("内层核心应收到全部 2 条: %d", logs())
	}
}

func newObservedZap(t *testing.T, s *Sink) (*zap.Logger, func() int) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	counting := zapcore.RegisterHooks(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(discardWriter{}),
		zapcore.DebugLevel,
	), func(zapcore.Entry) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})
	logger := zap.New(NewZapCore(counting, s))
	return logger, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
