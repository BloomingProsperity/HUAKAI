package queuewait

import "sync"

// Key 把等待容量限定在单租户、单池组、单账号范围内。
type Key struct {
	TenantID    int64
	PoolGroupID int64
	AccountID   int64
}

// Tracker 维护进程内等待位计数。它只表达本进程上限，不承担跨副本全局队列语义。
type Tracker struct {
	mu    sync.Mutex
	depth map[Key]int
}

func NewTracker() *Tracker {
	return &Tracker{depth: make(map[Key]int)}
}

// TryAcquire 尝试占用一个等待位。MaxWaiting<=0 表示没有等待位，立即拒绝。
func (t *Tracker) TryAcquire(key Key, maxWaiting int) (func(), bool) {
	if maxWaiting <= 0 {
		return nil, false
	}
	if t == nil {
		t = NewTracker()
	}
	t.mu.Lock()
	if t.depth == nil {
		t.depth = make(map[Key]int)
	}
	current := t.depth[key]
	if current+1 > maxWaiting {
		t.mu.Unlock()
		return nil, false
	}
	t.depth[key] = current + 1
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			current := t.depth[key]
			if current <= 1 {
				delete(t.depth, key)
				return
			}
			t.depth[key] = current - 1
		})
	}, true
}

func (t *Tracker) Depth(key Key) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.depth[key]
}
