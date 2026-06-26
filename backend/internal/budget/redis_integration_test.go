//go:build integration_redis

package budget

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisStoreUsesServerMinuteAndResetsOnBoundary(t *testing.T) {
	// 变异检查:如果 key 用本地调用方时间而非 Lua TIME 构造,时钟偏差的调用方
	// 会落到不同窗口,两者在 limit 1 下都会通过。
	ctx := context.Background()
	store := redisStoreForTest(t)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{RPM: 1},
	})
	req := reserveFixture(1101, 1, 11, 0)
	req.At = time.Unix(0, 0).UTC()
	if _, err := svc.Reserve(ctx, req); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	req2 := reserveFixture(1101, 2, 11, 0)
	req2.At = time.Unix(3600, 0).UTC()
	if _, err := svc.Reserve(ctx, req2); !IsDenied(err) {
		t.Fatalf("second err=%v, want same Redis-server minute deny", err)
	}

	sec := time.Now().UTC().Unix()
	wait := 61 - sec%60
	time.Sleep(time.Duration(wait) * time.Second)
	if _, err := svc.Reserve(ctx, reserveFixture(1101, 3, 11, 0)); err != nil {
		t.Fatalf("post-boundary reserve: %v", err)
	}
}

func TestRedisStoreConcurrentLimitIsExact(t *testing.T) {
	// 变异检查:把 Lua 换成 GET+INCR 在并发负载下会放行超过 50 个,因为竞争者
	// 在读取和自增之间发生竞态。
	ctx := context.Background()
	store := redisStoreForTest(t)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{RPM: 50},
	})

	var wg sync.WaitGroup
	results := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := svc.Reserve(ctx, reserveFixture(1201, int64(i+1), 12, 0))
			results <- err == nil && res.Allowed
		}(i)
	}
	wg.Wait()
	close(results)
	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != 50 {
		t.Fatalf("allowed=%d want exactly 50", allowed)
	}
}

func redisStoreForTest(t *testing.T) *RedisStore {
	t.Helper()
	url := os.Getenv("HUAKAI_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/0"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	opts.DB = 15
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping %s: %v", url, err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("redis flush test db: %v", err)
	}
	store := NewRedisStore(client)
	t.Cleanup(func() {
		_ = client.FlushDB(ctx).Err()
		if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
			t.Logf("redis close: %v", err)
		}
	})
	return store
}
