package httpkeepalive

import (
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

type fakeWriter struct {
	mu       sync.Mutex
	data     []byte
	flushes  int
	attempts int
	writeN   int
	writeErr error
}

var (
	_ io.Writer    = (*fakeWriter)(nil)
	_ http.Flusher = (*fakeWriter)(nil)
)

func (w *fakeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.attempts++
	if w.writeErr != nil {
		n := w.writeN
		if n < 0 {
			n = 0
		}
		if n > len(p) {
			n = len(p)
		}
		w.data = append(w.data, p[:n]...)
		return n, w.writeErr
	}

	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *fakeWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.flushes++
}

func (w *fakeWriter) byteCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.data)
}

func (w *fakeWriter) snapshot() ([]byte, int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	data := append([]byte(nil), w.data...)
	return data, w.flushes, w.attempts
}

func waitForByteCount(w *fakeWriter, minimum int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w.byteCount() >= minimum {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}

	return w.byteCount() >= minimum
}

func TestStartWithNonPositiveIntervalIsLazy(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Millisecond} {
		t.Run(interval.String(), func(t *testing.T) {
			w := &fakeWriter{}
			k := Start(w, interval)

			time.Sleep(40 * time.Millisecond)

			k.Stop()

			data, flushes, attempts := w.snapshot()
			if len(data) != 0 {
				t.Fatalf("惰性保活写入了 %d 个字节", len(data))
			}
			if attempts != 0 {
				t.Fatalf("惰性保活发生了 %d 次写入尝试", attempts)
			}
			if flushes != 0 {
				t.Fatalf("惰性保活发生了 %d 次刷新", flushes)
			}
			if k.Wrote() {
				t.Fatal("惰性保活不得报告已写入")
			}
		})
	}
}

func TestKeepaliveWritesNewlinesAndFlushes(t *testing.T) {
	w := &fakeWriter{}
	k := Start(w, 20*time.Millisecond)
	defer k.Stop()

	if !waitForByteCount(w, 1, 500*time.Millisecond) {
		t.Fatal("等待保活字节超时")
	}
	time.Sleep(50 * time.Millisecond)

	k.Stop()

	data, flushes, attempts := w.snapshot()
	if len(data) == 0 {
		t.Fatal("没有收到保活字节")
	}
	for i, b := range data {
		if b != '\n' {
			t.Fatalf("第 %d 个保活字节为 %q，不是换行", i, b)
		}
	}
	if flushes != len(data) {
		t.Fatalf("写入了 %d 个字节，但只刷新了 %d 次", len(data), flushes)
	}
	if attempts != len(data) {
		t.Fatalf("成功写入 %d 个字节，但写入尝试次数为 %d", len(data), attempts)
	}
	if !k.Wrote() {
		t.Fatal("成功保活后必须报告已写入")
	}
}

func TestStopPreventsFurtherWrites(t *testing.T) {
	w := &fakeWriter{}
	k := Start(w, 10*time.Millisecond)
	defer k.Stop()

	if !waitForByteCount(w, 1, 500*time.Millisecond) {
		t.Fatal("等待首个保活字节超时")
	}

	k.Stop()
	before := w.byteCount()

	select {
	case <-k.doneCh:
	default:
		t.Fatal("Stop 返回时后台任务尚未结束")
	}

	time.Sleep(50 * time.Millisecond)

	after := w.byteCount()
	if after != before {
		t.Fatalf("Stop 返回后字节数从 %d 增长到 %d", before, after)
	}
}

func TestFirstKeepaliveIsDelayed(t *testing.T) {
	w := &fakeWriter{}
	k := Start(w, 100*time.Millisecond)
	defer k.Stop()

	time.Sleep(30 * time.Millisecond)
	k.Stop()

	data, flushes, attempts := w.snapshot()
	if len(data) != 0 {
		t.Fatalf("首个 interval 前写入了 %d 个字节", len(data))
	}
	if attempts != 0 {
		t.Fatalf("首个 interval 前发生了 %d 次写入尝试", attempts)
	}
	if flushes != 0 {
		t.Fatalf("首个 interval 前发生了 %d 次刷新", flushes)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	w := &fakeWriter{}
	k := Start(w, time.Hour)

	k.Stop()
	k.Stop()

}

func TestStopWaitsForBackgroundExit(t *testing.T) {
	k := &Keepalive{
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(k.doneCh)
		})
	}
	defer release()

	returned := make(chan struct{})
	go func() {
		k.Stop()
		close(returned)
	}()

	select {
	case <-k.stopCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop 未发送停止通知")
	}

	select {
	case <-returned:
		t.Fatal("后台退出信号发出前 Stop 已返回")
	case <-time.After(30 * time.Millisecond):
	}

	release()

	select {
	case <-returned:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("后台退出信号发出后 Stop 仍未返回")
	}
}

type fakeWriteError struct{}

func (fakeWriteError) Error() string {
	return "模拟写入失败"
}

func TestWriteErrorStopsKeepalive(t *testing.T) {
	w := &fakeWriter{writeErr: fakeWriteError{}}
	k := Start(w, 10*time.Millisecond)
	defer k.Stop()

	select {
	case <-k.doneCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("写入失败后后台任务没有退出")
	}

	time.Sleep(30 * time.Millisecond)

	data, flushes, attempts := w.snapshot()
	if attempts != 1 {
		t.Fatalf("写入失败后仍继续尝试，共尝试 %d 次", attempts)
	}
	if len(data) != 0 {
		t.Fatalf("失败写入记录了 %d 个字节", len(data))
	}
	if flushes != 0 {
		t.Fatalf("失败写入后发生了 %d 次刷新", flushes)
	}
	if k.Wrote() {
		t.Fatal("失败写入不得报告已向客户端写入")
	}
}

func TestWriteErrorAfterDeliveredByteReportsDelivery(t *testing.T) {
	w := &fakeWriter{writeN: 1, writeErr: fakeWriteError{}}
	k := Start(w, 10*time.Millisecond)
	defer k.Stop()

	select {
	case <-k.doneCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("部分成功写入后后台任务没有退出")
	}

	data, flushes, attempts := w.snapshot()
	if attempts != 1 || len(data) != 1 {
		t.Fatalf("写入尝试/已交付字节=%d/%d want 1/1", attempts, len(data))
	}
	if flushes != 0 {
		t.Fatalf("带错误的写入不应主动刷新，实际刷新 %d 次", flushes)
	}
	if !k.Wrote() {
		t.Fatal("已交付字节即使伴随错误，也必须报告已向客户端写入")
	}
}
