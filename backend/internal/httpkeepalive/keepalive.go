package httpkeepalive

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// Keepalive 管理非流式响应的周期性保活写入。
type Keepalive struct {
	mu       sync.Mutex
	w        io.Writer
	written  int64
	stopped  bool
	started  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// Start 启动后台保活任务。首个换行会在第一个 interval 到达后写入。
func Start(w io.Writer, interval time.Duration) *Keepalive {
	k := &Keepalive{
		w:      w,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	if interval <= 0 {
		k.stopped = true
		close(k.doneCh)
		return k
	}

	ticker := time.NewTicker(interval)
	go k.run(ticker)

	return k
}

func (k *Keepalive) run(ticker *time.Ticker) {
	defer func() {
		ticker.Stop()
		close(k.doneCh)
	}()

	for {
		select {
		case <-k.stopCh:
			return
		case <-ticker.C:
			if !k.writeKeepalive() {
				return
			}
		}
	}
}

func (k *Keepalive) writeKeepalive() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.stopped {
		return false
	}

	n, err := k.w.Write([]byte{'\n'})
	if n > 0 {
		k.written += int64(n)
	}
	if err != nil || n != 1 {
		k.stopped = true
		return false
	}
	k.started = true

	if flusher, ok := k.w.(http.Flusher); ok {
		flusher.Flush()
	}

	return true
}

// Started 报告是否已有保活字节成功写给客户端。executor 用它守住
// “任何字节交付后不得重放”的边界。
func (k *Keepalive) Started() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.started
}

// Stop 停止保活并等待后台任务完全退出。重复调用是安全的。
func (k *Keepalive) Stop() {
	k.stopOnce.Do(func() {
		k.mu.Lock()
		k.stopped = true
		close(k.stopCh)
		k.mu.Unlock()
	})

	<-k.doneCh
}

// Wrote 报告本次保活是否已经向客户端写过字节。
func (k *Keepalive) Wrote() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.written > 0
}
