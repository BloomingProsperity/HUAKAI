package workerlease

import (
	"context"
	"os"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestPostgresLeaseRequiresPool(t *testing.T) {
	if acquired, release, err := NewPostgres(nil, 1, "test").TryAcquire(context.Background()); err == nil || acquired || release != nil {
		t.Fatalf("nil pool result acquired=%v release=%v err=%v", acquired, release != nil, err)
	}
	if acquired, session, err := NewPostgres(nil, 1, "test").TryAcquireSession(context.Background()); err == nil || acquired || session != nil {
		t.Fatalf("nil pool session result acquired=%v session=%v err=%v", acquired, session != nil, err)
	}
}

func TestPostgresLeaseMutualExclusion(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("未配置 HUAKAI_DATABASE_URL，跳过 PostgreSQL 租约集成测试")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	defer pool.Close()

	lease := NewPostgres(pool, 0x48554B544553544C, "test")
	first, releaseFirst, err := lease.TryAcquire(ctx)
	if err != nil || !first {
		t.Fatalf("第一次抢租约 acquired=%v err=%v", first, err)
	}
	second, releaseSecond, err := lease.TryAcquire(ctx)
	if err != nil || second || releaseSecond != nil {
		releaseFirst()
		t.Fatalf("持有期间第二次抢租约 acquired=%v release=%v err=%v", second, releaseSecond != nil, err)
	}
	releaseFirst()
	releaseFirst()
	third, releaseThird, err := lease.TryAcquire(ctx)
	if err != nil || !third {
		t.Fatalf("释放后再次抢租约 acquired=%v err=%v", third, err)
	}
	releaseThird()
}

func TestPostgresSessionLeaseHealthAndRelease(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("未配置 HUAKAI_DATABASE_URL，跳过 PostgreSQL 租约集成测试")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	defer pool.Close()

	lease := NewPostgres(pool, 0x48554B534553534E, "session_test")
	acquired, session, err := lease.TryAcquireSession(ctx)
	if err != nil || !acquired || session == nil {
		t.Fatalf("取得会话租约 acquired=%v session=%v err=%v", acquired, session != nil, err)
	}
	if err := session.Healthy(ctx); err != nil {
		session.Release()
		t.Fatalf("租约会话健康检查: %v", err)
	}
	second, secondSession, err := lease.TryAcquireSession(ctx)
	if err != nil || second || secondSession != nil {
		session.Release()
		t.Fatalf("持有期间不应取得第二份租约 acquired=%v session=%v err=%v", second, secondSession != nil, err)
	}
	session.Release()
	session.Release()

	third, thirdSession, err := lease.TryAcquireSession(ctx)
	if err != nil || !third || thirdSession == nil {
		t.Fatalf("释放后再次取得会话租约 acquired=%v session=%v err=%v", third, thirdSession != nil, err)
	}
	thirdSession.Release()
}
