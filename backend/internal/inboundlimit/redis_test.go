package inboundlimit

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNormalizeTierRejectsRedisKeyInjection(t *testing.T) {
	for _, value := range []string{"global:other", "global/other", "空值"} {
		if got := normalizeTier(value); got != "" {
			t.Fatalf("normalizeTier(%q)=%q，应拒绝", value, got)
		}
	}
	if got := normalizeTier(" Auth_Login "); got != "auth_login" {
		t.Fatalf("normalizeTier=%q", got)
	}
}

func TestBucketTTLHasBounds(t *testing.T) {
	if got := bucketTTL(Limit{RatePerSecond: 100, Burst: 1}); got != time.Minute {
		t.Fatalf("最小 TTL=%s", got)
	}
	if got := bucketTTL(Limit{RatePerSecond: 0.000001, Burst: 100}); got != 24*time.Hour {
		t.Fatalf("最大 TTL=%s", got)
	}
}

func TestValidLimitRejectsUnsafeNumbers(t *testing.T) {
	for _, limit := range []Limit{
		{},
		{RatePerSecond: -1, Burst: 1},
		{RatePerSecond: 1, Burst: 0.5},
	} {
		if validLimit(limit) {
			t.Fatalf("非法限流参数被接受: %+v", limit)
		}
	}
	if !validLimit(Limit{RatePerSecond: 1, Burst: 1}) {
		t.Fatal("合法限流参数被拒绝")
	}
}

func TestRedisStoreTokenBucketIntegration(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("HUAKAI_REDIS_URL"))
	if url == "" {
		t.Skip("未配置 HUAKAI_REDIS_URL，跳过 Redis 原子限流集成测试")
	}
	ctx := context.Background()
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("解析 Redis URL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("连接 Redis: %v", err)
	}
	store := NewRedisStore(client, "")
	store.prefix = "huakai:test:inbound:" + strings.ReplaceAll(t.Name(), "/", "_") +
		":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		keys := []string{
			store.redisKey("global", "198.51.100.7"),
			store.redisKey("global", "198.51.100.8"),
			store.redisKey("auth_login", "198.51.100.7"),
		}
		if err := client.Del(context.Background(), keys...).Err(); err != nil {
			t.Errorf("清理 Redis 测试桶: %v", err)
		}
	})

	limit := Limit{RatePerSecond: 0.01, Burst: 2}
	first, err := store.Allow(ctx, "global", "198.51.100.7", limit)
	if err != nil || !first.Allowed {
		t.Fatalf("第一次消费 allowed=%v err=%v", first.Allowed, err)
	}
	second, err := store.Allow(ctx, "global", "198.51.100.7", limit)
	if err != nil || !second.Allowed {
		t.Fatalf("第二次消费 allowed=%v err=%v", second.Allowed, err)
	}
	third, err := store.Allow(ctx, "global", "198.51.100.7", limit)
	if err != nil {
		t.Fatalf("第三次消费: %v", err)
	}
	if third.Allowed || third.RetryAfter < time.Second {
		t.Fatalf("第三次必须被共享桶拒绝，decision=%+v", third)
	}

	other, err := store.Allow(ctx, "global", "198.51.100.8", limit)
	if err != nil || !other.Allowed {
		t.Fatalf("不同主体不得串桶 allowed=%v err=%v", other.Allowed, err)
	}
	otherTier, err := store.Allow(ctx, "auth_login", "198.51.100.7", limit)
	if err != nil || !otherTier.Allowed {
		t.Fatalf("不同层不得串桶 allowed=%v err=%v", otherTier.Allowed, err)
	}
}
