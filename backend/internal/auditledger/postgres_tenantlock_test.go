package auditledger

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestLockTenantWriter_ReclaimsDistinctTenants 抓对抗 bug-hunt S3:
// 每个历史出现过的租户都懒建一把进程内写锁,若释放后不回收,tenantLocks map 会随历史租户数无界增长。
// 顺序获取并释放 100 个不同租户的锁后,map 必须回收至 0。
// §14 变异:删 lockTenantWriter 释放路径里的 delete → 100 条残留 → len!=0 → 测试红。
func TestLockTenantWriter_ReclaimsDistinctTenants(t *testing.T) {
	l := &PostgresLedger{tenantLocks: make(map[int64]*tenantLockEntry)}
	for tid := int64(1); tid <= 100; tid++ {
		unlock := l.lockTenantWriter(tid)
		unlock()
	}
	l.tenantMu.Lock()
	n := len(l.tenantLocks)
	l.tenantMu.Unlock()
	if n != 0 {
		t.Fatalf("100 个不同租户全部释放后 tenantLocks 应回收至 0, 实得 %d", n)
	}
}

// TestLockTenantWriter_SerializesAndReclaims 验证引用计数回收不破坏「同租户写者互斥」这一根本约束,
// 且并发全部释放后条目仍被回收(refs 在并发竞争下也能正确归零)。
// -race 下若互斥失效会直接报数据竞争;§14 变异:删 delete → 结尾 map 非空 → 红;删 e.mu.Lock 串行
// → counter 竞争/错值或 -race 报警 → 红。
func TestLockTenantWriter_SerializesAndReclaims(t *testing.T) {
	l := &PostgresLedger{tenantLocks: make(map[int64]*tenantLockEntry)}
	const tid = int64(7)
	const g = 50
	var counter int
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := l.lockTenantWriter(tid)
			counter++ // 受 per-tenant 锁保护,不应有数据竞争
			unlock()
		}()
	}
	wg.Wait()
	if counter != g {
		t.Fatalf("counter=%d want %d(同租户写者串行失效?)", counter, g)
	}
	l.tenantMu.Lock()
	n := len(l.tenantLocks)
	l.tenantMu.Unlock()
	if n != 0 {
		t.Fatalf("并发全部释放后 tenantLocks 应回收至 0, 实得 %d", n)
	}
}

// TestLockTenantWriter_NeverTwoHeldSameTenant 对抗交错:同一租户高频 lock/unlock 使 refs 反复归零/重建,
// 断言任意时刻进入临界区的写者数恒 ≤1。专门锁死正确性命门「refs++ 必须在释放 tenantMu 之前完成」——
// 若把 refs++ 移到 tenantMu.Unlock 之后,持有者尚在时条目会被并发 delete/重建,出现同租户两把锁并行 →
// inFlight 抓到 >1(且 -race 报警)。现有两测(纯顺序 / 高争用 refs 几乎不归零)对该回归不敏感,故补此条。
func TestLockTenantWriter_NeverTwoHeldSameTenant(t *testing.T) {
	l := &PostgresLedger{tenantLocks: make(map[int64]*tenantLockEntry)}
	const tid = int64(9)
	const g = 64
	const iter = 2000
	var inFlight, maxSeen, bad int32
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iter; j++ {
				unlock := l.lockTenantWriter(tid)
				n := atomic.AddInt32(&inFlight, 1)
				if n > 1 {
					atomic.StoreInt32(&bad, 1)
				}
				for { // 记录峰值,仅用于诊断
					m := atomic.LoadInt32(&maxSeen)
					if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
						break
					}
				}
				atomic.AddInt32(&inFlight, -1)
				unlock()
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&bad) != 0 {
		t.Fatalf("同租户曾出现 >1 个并发临界区(峰值 %d)→ 互斥/回收交错被破坏", atomic.LoadInt32(&maxSeen))
	}
	l.tenantMu.Lock()
	n := len(l.tenantLocks)
	l.tenantMu.Unlock()
	if n != 0 {
		t.Fatalf("高频交错后 map 应回收至 0, 实得 %d", n)
	}
}
