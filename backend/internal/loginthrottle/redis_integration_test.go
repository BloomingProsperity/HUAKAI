package loginthrottle

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisStoreReservationAndBanIntegration(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("HUAKAI_REDIS_URL"))
	if rawURL == "" {
		t.Skip("未配置 HUAKAI_REDIS_URL，跳过 Redis 登录节流集成测试")
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("解析 Redis URL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("连接 Redis: %v", err)
	}

	store := NewRedisStore(client)
	store.prefix = "huakai:test:login:" + strings.ReplaceAll(t.Name(), "/", "_") + ":" + time.Now().UTC().Format("150405.000000000")
	cfg := Config{
		InFlightLimit: 2,
		Window:        time.Minute,
		WindowLimit:   10,
		BanWindow:     time.Minute,
		BanAfter:      2,
		BanDuration:   time.Minute,
		InFlightTTL:   time.Minute,
	}

	first, decision, err := store.Reserve(ctx, "198.51.100.40", cfg)
	if err != nil || !decision.Allowed || first == nil {
		t.Fatalf("第一次预留 decision=%+v reservation=%v err=%v", decision, first != nil, err)
	}
	second, decision, err := store.Reserve(ctx, "198.51.100.40", cfg)
	if err != nil || !decision.Allowed || second == nil {
		t.Fatalf("第二次预留 decision=%+v reservation=%v err=%v", decision, second != nil, err)
	}
	third, decision, err := store.Reserve(ctx, "198.51.100.40", cfg)
	if err != nil || decision.Allowed || decision.Reason != ReasonIPInFlight || third != nil {
		t.Fatalf("第三次必须命中共享并发上限 decision=%+v reservation=%v err=%v", decision, third != nil, err)
	}
	if err := first.Failure(ctx); err != nil {
		t.Fatalf("第一次失败提交: %v", err)
	}
	if err := first.Failure(ctx); err != nil {
		t.Fatalf("重复失败提交必须幂等: %v", err)
	}
	if err := second.Cancel(ctx); err != nil {
		t.Fatalf("取消第二个 reservation: %v", err)
	}

	fourth, decision, err := store.Reserve(ctx, "198.51.100.40", cfg)
	if err != nil || !decision.Allowed || fourth == nil {
		t.Fatalf("释放后应再次允许 decision=%+v reservation=%v err=%v", decision, fourth != nil, err)
	}
	if err := fourth.Failure(ctx); err != nil {
		t.Fatalf("第二次失败提交: %v", err)
	}
	banned, decision, err := store.Reserve(ctx, "198.51.100.40", cfg)
	if err != nil || decision.Allowed || decision.Reason != ReasonIPBanned || banned != nil {
		t.Fatalf("累计失败后必须封禁 decision=%+v reservation=%v err=%v", decision, banned != nil, err)
	}
	if decision.RetryAfter < time.Second {
		t.Fatalf("封禁必须返回可执行 Retry-After: %s", decision.RetryAfter)
	}
}
