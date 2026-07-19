package usersession

import (
	"context"
	"sync"
	"testing"
	"time"
)

// slowPolicyListStore 在 ListActiveFamiliesForDevicePolicy 读完计数后人为停顿,
// 把 enforceDevicePolicy(数活跃 family)与 CreateFamily(插新 family)之间的
// TOCTOU 窗口撑开, 让并发登录的越限竞态从"偶发"变"稳定复现"。
// 修复(判定+建家族原子化)后, 该停顿会被串行化, 测试仍然收敛且不死锁。
type slowPolicyListStore struct {
	*MemoryStore
	delay time.Duration
}

func (s *slowPolicyListStore) ListActiveFamiliesForDevicePolicy(
	ctx context.Context,
	tenantID, userID int64,
	limit int,
) ([]SessionFamily, error) {
	families, err := s.MemoryStore.ListActiveFamiliesForDevicePolicy(ctx, tenantID, userID, limit)
	// 计数已快照, 停顿模拟真实 DB round-trip; 缺原子保护时并发者都读到"未满"。
	time.Sleep(s.delay)
	return families, err
}

// TestB7DeviceLimitConcurrentCreateDoesNotExceedMax 断言【正确行为】: 当设备上限
// MaxActiveFamilies=N 且策略为默认拒绝时, 同一 (tenant,user) 的 M 个并发 Create
// 结束后, 活跃 family 数绝不超过 N。有缺陷的代码(count→create 非原子, TOCTOU)
// 会让多数并发者都通过判定各自建 family, 活跃数冲到 M > N → RED。
func TestB7DeviceLimitConcurrentCreateDoesNotExceedMax(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	const (
		tenantID    = int64(1)
		userID      = int64(77)
		maxFamilies = 2
		concurrency = 8
	)

	store := &slowPolicyListStore{MemoryStore: NewMemoryStore(), delay: 20 * time.Millisecond}
	svc := NewService(store)
	svc.Now = func() time.Time { return base }
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = maxFamilies
	// 默认策略(空 DevicePolicy)= 满则拒绝 ErrDeviceLimitExceeded, 不撤旧不放行。

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.Create(ctx, CreateInput{
				TenantID: tenantID, UserID: userID,
				IP: "10.0.0.1", UserAgent: "Chrome/1",
			})
		}()
	}
	close(start)
	wg.Wait()

	active, err := store.MemoryStore.ListActiveFamiliesForDevicePolicy(ctx, tenantID, userID, concurrency+1)
	if err != nil {
		t.Fatalf("ListActiveFamiliesForDevicePolicy: %v", err)
	}
	if len(active) > maxFamilies {
		t.Fatalf("并发 Create 后活跃 family=%d 超过 MaxActiveFamilies=%d (设备上限 TOCTOU 越限)", len(active), maxFamilies)
	}
}
