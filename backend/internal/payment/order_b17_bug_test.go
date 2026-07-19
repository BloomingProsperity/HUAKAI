// HUAKAI · iKun

package payment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// toctouBarrier 一次性栅栏: 当 n 个 goroutine 都到达后同时放行, 用来把 OpenRecharge 的
// check-then-act 竞态窗口开到最大 —— 所有并发请求都读完 pending 计数(全部看到 0)之后,
// 才允许任何一个继续去 CreateOrder。
type toctouBarrier struct {
	mu    sync.Mutex
	n     int
	count int
	ready chan struct{}
}

func newToctouBarrier(n int) *toctouBarrier {
	return &toctouBarrier{n: n, ready: make(chan struct{})}
}

func (b *toctouBarrier) wait() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		close(b.ready)
	}
	b.mu.Unlock()
	<-b.ready
}

// toctouProbeStore 在 pending 计数读取后插入栅栏, 确定性地复现 B17 的 TOCTOU:
// 所有并发 OpenRecharge 都完成读取(读到 pending=0)后, 才各自去建单。
type toctouProbeStore struct {
	*MemoryStore
	barrier *toctouBarrier
}

func (s *toctouProbeStore) CountPendingOrders(ctx context.Context, tenantID, userID int64, now time.Time) (int, error) {
	n, err := s.MemoryStore.CountPendingOrders(ctx, tenantID, userID, now)
	s.barrier.wait()
	return n, err
}

// TestOpenRecharge_B17_ConcurrentPendingLimitNotBypassable 断言【正确行为】:
// 无论多少并发充值请求命中同一用户, 落库的 pending 订单数都不得超过 MaxPendingPerUser。
// 当前 check-then-act 实现下, 读(countPendingOrders)与写(CreateOrder)分处两段无锁事务,
// N 个请求全部读到 pending<limit 后各自建单 → pending 超过上限 → 本测试应 RED。
func TestOpenRecharge_B17_ConcurrentPendingLimitNotBypassable(t *testing.T) {
	ctx := context.Background()
	const workers = 6
	const maxPending = 1
	store := &toctouProbeStore{MemoryStore: NewMemoryStore(), barrier: newToctouBarrier(workers)}
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	svc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return now }))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var success, pendingRejected, otherErr int
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.OpenRecharge(ctx, OpenInput{
				TenantID:          1,
				UserID:            2,
				Provider:          "test",
				ExternalTradeNo:   "b17-" + stringID(i),
				Amount:            decimal.RequireFromString("1.00"),
				CurrencyCode:      "USD",
				MaxPendingPerUser: maxPending,
				Now:               now,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrPendingLimit):
				pendingRejected++
			default:
				otherErr++
			}
		}(i)
	}
	wg.Wait()

	if otherErr != 0 {
		t.Fatalf("unexpected non-limit errors count=%d (success=%d rejected=%d)", otherErr, success, pendingRejected)
	}
	pending, err := store.MemoryStore.CountPendingOrders(ctx, 1, 2, now)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	// 权威不变量: pending 订单数不得超过 MaxPendingPerUser。
	if pending > maxPending {
		t.Fatalf("pending orders=%d exceeds MaxPendingPerUser=%d: concurrent TOCTOU cap bypass (success=%d rejected=%d)",
			pending, maxPending, success, pendingRejected)
	}
	if success != maxPending {
		t.Fatalf("successful OpenRecharge=%d want exactly %d (rejected=%d)", success, maxPending, pendingRejected)
	}
}
