package twofa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// barrierStore 包一层真实 MemoryStore,只加一个确定性的"读栅栏":并发失败请求的所有
// GetSettings 读必须先全部完成,任何失败写(MarkFailure/RecordFailure)才放行。这精确
// 复现审计点名的读改写竞态时序——N 个请求都读到同一个旧计数 k,再各自写回——无需依赖
// 调度抖动,红/绿都确定。存储真值一律委托给内嵌 MemoryStore,故 MarkFailure 的"写绝对值"
// 与 RecordFailure 的"原子 +1"语义与生产实现一致。
type barrierStore struct {
	*MemoryStore
	n       int
	armed   atomic.Bool
	reads   atomic.Int32
	barrier chan struct{}
	once    sync.Once
}

func newBarrierStore(n int) *barrierStore {
	return &barrierStore{MemoryStore: NewMemoryStore(), n: n, barrier: make(chan struct{})}
}

func (b *barrierStore) arm() { b.armed.Store(true) }

func (b *barrierStore) GetSettings(ctx context.Context, tenantID, userID int64) (Settings, bool, error) {
	s, ok, err := b.MemoryStore.GetSettings(ctx, tenantID, userID)
	if b.armed.Load() {
		if int(b.reads.Add(1)) == b.n {
			b.once.Do(func() { close(b.barrier) })
		}
	}
	return s, ok, err
}

func (b *barrierStore) waitReads() {
	if b.armed.Load() {
		<-b.barrier
	}
}

func (b *barrierStore) MarkFailure(ctx context.Context, tenantID, userID int64, failedAttempts int, lockedUntil *time.Time, now time.Time) error {
	b.waitReads()
	return b.MemoryStore.MarkFailure(ctx, tenantID, userID, failedAttempts, lockedUntil, now)
}

func (b *barrierStore) RecordFailure(ctx context.Context, tenantID, userID int64, lockThreshold int, lockedUntil *time.Time, now time.Time) (int, bool, error) {
	b.waitReads()
	return b.MemoryStore.RecordFailure(ctx, tenantID, userID, lockThreshold, lockedUntil, now)
}

// TestConcurrentFailedLoginsCannotBypassLockout 守审计 B1 的 [S2]:2FA 失败计数必须原子自增。
// 攻击者已知密码(2FA 正是防这一场景),用一枚 5 分钟内可复用的 challenge 并发轰 N 个错误
// code。正确行为:N 次失败累加到 N=maxFailedAttempts,账号必须锁定。
// 缺陷版本 recordFailure 从旧快照算 failed=settings.FailedAttempts+1 再走绝对 SET,N 个请求
// 都读到 0、都写回 1,计数停在 1,锁定门永不触发——本测试断言"锁定"于是变红。
func TestConcurrentFailedLoginsCannotBypassLockout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	const maxFailed = 5
	store := newBarrierStore(maxFailed)
	svc := NewService(
		store,
		mustKeyProvider(t),
		WithNow(func() time.Time { return now }),
		WithMaxFailedAttempts(maxFailed),
		WithLockDuration(15*time.Minute),
	)
	setupAndEnable(t, ctx, svc, now)

	// N 个并发错误 code,模拟同一 challenge 5 分钟窗口内的暴力猜测。
	store.arm()
	var wg sync.WaitGroup
	errs := make([]error, maxFailed)
	for i := 0; i < maxFailed; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"})
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	store.armed.Store(false)

	for i, err := range errs {
		if !errors.Is(err, ErrInvalidCode) && !errors.Is(err, ErrLocked) {
			t.Fatalf("并发失败请求 %d 返回意外错误 err=%v(既非 ErrInvalidCode 也非 ErrLocked)", i, err)
		}
	}

	// 正确行为:maxFailed 次失败后账号必须锁定。
	status, err := svc.Status(ctx, 1, 1001)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LockedUntil == nil {
		t.Fatalf("并发暴力绕过锁定:%d 次失败后账号仍未锁定(失败计数被读改写竞态覆盖,停在 <%d)", maxFailed, maxFailed)
	}

	// 锁定生效后,即便再来一次也应被 ErrLocked 挡住。
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("锁定后再次校验 err=%v want ErrLocked", err)
	}
}
