//go:build integration_pg

// B11 [S3] 判别测试：binding 并发 acquire 不得在等待会话级 advisory lock 期间
// 一直占着 pgxpool 连接，否则单个热点 binding 的并发等待者会占满整个进程级
// 连接池，饿死其它租户/binding（网关级停摆）。
//
// 机理复现：把连接池调到很小（MaxConns 少），外部先持住某个 binding 的
// pool_binding_concurrency advisory lock，再向【同一个 binding】打入远多于池
// 容量的并发 mgr.Acquire（MaxParallelRequests>0）。
//   - 旧的有缺陷实现：acquireOnce 先 pool.Acquire 拿连接、再对阻塞式
//     pg_advisory_lock 死等，等待者把池里所有连接全部占住 → 一个无关的
//     控制性 pool.Acquire 拿不到连接，在其 deadline 内超时 → 本测试 RED。
//   - 修复后：acquireBindingLockConn 用非阻塞 pg_try_advisory_lock，拿不到锁
//     立刻把连接还回池再退避重试，控制性 pool.Acquire 能在 deadline 内拿到
//     连接 → 本测试 GREEN。
//
// 需要真实 PostgreSQL（advisory lock 只有真库才有），故 //go:build integration_pg。
// 设 HUAKAI_DATABASE_URL 后：go test -tags integration_pg ./internal/pool/ -run B11

package pool

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// bindingConcurrencyLockSQL 逐字对齐生产的 AcquireBindingConcurrencyLock 锁键，
// 让测试的外部持锁者与被测代码命中同一把会话级锁。
const bindingConcurrencyLockSQL = `SELECT pg_advisory_lock(hashtextextended('pool_binding_concurrency'::text, $1::bigint))`
const bindingConcurrencyUnlockSQL = `SELECT pg_advisory_unlock(hashtextextended('pool_binding_concurrency'::text, $1::bigint))`

func TestDBSlotManager_B11_HotBindingDoesNotStarvePool(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 小连接池：把「连接稀缺」这个前提做实，让占连接的等待者能占满全池。
	const maxConns = 6
	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: maxConns, MinConns: 1})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedAdapterGraph(t, ctx, pgPool, "b11-starve")

	// 外部持住该 binding 的会话级锁，制造持续的锁竞争。占用 1 条连接。
	holderConn, err := pgPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	if _, err := holderConn.Exec(ctx, bindingConcurrencyLockSQL, seed.bindingID); err != nil {
		holderConn.Release()
		t.Fatalf("hold binding lock: %v", err)
	}
	holderReleased := false
	releaseHolder := func() {
		if holderReleased {
			return
		}
		holderReleased = true
		_, _ = holderConn.Exec(context.Background(), bindingConcurrencyUnlockSQL, seed.bindingID)
		holderConn.Release()
	}
	defer releaseHolder()

	mgr := NewDBSlotManager(pgPool)
	acct := &AccountSnapshot{ID: seed.providerAccountID, TenantID: seed.tenantID, MaxConcurrency: 1000}

	// 远多于池容量的并发等待者，全部打向【同一个】热点 binding。
	const waiters = 12
	waiterCtx, waiterCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer waiterCancel()
	var wg sync.WaitGroup
	wg.Add(waiters)
	for i := 0; i < waiters; i++ {
		go func(seq int) {
			defer wg.Done()
			// 拿不到锁就一直等（旧实现占着连接，新实现还回连接再退避）。
			_, _ = mgr.Acquire(waiterCtx, acct, SelectionRequest{
				TenantID:            seed.tenantID,
				BindingID:           seed.bindingID,
				ClaimID:             seed.claimID,
				MaxParallelRequests: 1000,
				AttemptSeq:          seq,
			})
		}(i)
	}

	// 给等待者时间进入锁等待状态（旧实现：此刻已占满全池）。
	time.Sleep(500 * time.Millisecond)

	// 控制性探针：一个与热点 binding 无关的普通 pool.Acquire。正确行为下它应
	// 在很短的 deadline 内拿到连接；若热点 binding 的等待者把全池占死，它会超时。
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer probeCancel()
	probeConn, probeErr := pgPool.Acquire(probeCtx)
	if probeErr == nil {
		probeConn.Release()
	}

	// 收尾：先放锁让等待者跑完，再取消 waiterCtx，然后 join，避免泄漏 goroutine。
	releaseHolder()
	waiterCancel()
	wg.Wait()

	if probeErr != nil {
		t.Fatalf("B11: 一个无关的 pool.Acquire 在热点 binding 并发下被饿死（%v）——"+
			"等待会话级锁期间占着连接，单个热点 binding 拖垮了整个连接池", probeErr)
	}
}
