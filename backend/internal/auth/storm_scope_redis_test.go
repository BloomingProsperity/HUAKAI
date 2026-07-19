package auth

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisStormScopeStoreIntegration(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("HUAKAI_REDIS_URL"))
	if rawURL == "" {
		t.Skip("未配置 HUAKAI_REDIS_URL，跳过共享刷新预算集成测试")
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("解析 Redis URL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	prefix := "huakai:test:credential-storm:" + time.Now().UTC().Format("150405.000000000")
	first := NewRedisStormScopeStore(client, prefix)
	second := NewRedisStormScopeStore(client, prefix)
	ctx := context.Background()
	cfg := StormScopeConfig{PerEndpointRate: 0.001, PerEndpointBurst: 1}
	firstController := NewStormControllerWithSharedScopeBudget(nil, cfg, first)
	secondController := NewStormControllerWithSharedScopeBudget(nil, cfg, second)

	refund, outcome, err := firstController.AcquireProviderEndpoint(ctx, 1, "anthropic", "")
	if err != nil || outcome != "" || refund == nil {
		t.Fatalf("首个副本应取得令牌 outcome=%q refund=%v err=%v", outcome, refund != nil, err)
	}
	secondRefund, outcome, err := secondController.AcquireProviderEndpoint(ctx, 1, "anthropic", "")
	if err != nil || outcome != OutcomeStormBudgetExhausted || secondRefund != nil {
		t.Fatalf("第二个副本不得越过共享预算 outcome=%q refund=%v err=%v", outcome, secondRefund != nil, err)
	}
	refund()
	secondRefund, outcome, err = secondController.AcquireProviderEndpoint(ctx, 1, "anthropic", "")
	if err != nil || outcome != "" || secondRefund == nil {
		t.Fatalf("退回后应重新取得令牌 outcome=%q refund=%v err=%v", outcome, secondRefund != nil, err)
	}
	otherRefund, outcome, err := secondController.AcquireProviderEndpoint(ctx, 1, "openai", "")
	if err != nil || outcome != "" || otherRefund == nil {
		t.Fatalf("不同厂商端点必须隔离 outcome=%q refund=%v err=%v", outcome, otherRefund != nil, err)
	}
	anthropicKey, err := first.redisKey("provider_endpoint:anthropic|", cfg.PerEndpointRate, cfg.PerEndpointBurst)
	if err != nil {
		t.Fatalf("计算测试桶键: %v", err)
	}
	ttl, err := client.PTTL(ctx, anthropicKey).Result()
	if err != nil || ttl < 990*time.Second || ttl > 1000*time.Second {
		t.Fatalf("共享刷新桶 TTL=%v err=%v，期望接近完整 1000 秒回满窗口", ttl, err)
	}
	openAIKey, err := first.redisKey("provider_endpoint:openai|", cfg.PerEndpointRate, cfg.PerEndpointBurst)
	if err != nil {
		t.Fatalf("计算隔离测试桶键: %v", err)
	}
	if err := client.Del(ctx, anthropicKey, openAIKey).Err(); err != nil {
		t.Fatalf("清理共享刷新测试桶: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("关闭 Redis 客户端: %v", err)
	}
	failedRefund, outcome, err := firstController.AcquireProviderEndpoint(ctx, 1, "gemini", "")
	if err == nil || outcome != "" || failedRefund != nil {
		t.Fatalf("共享存储故障必须拒绝刷新 outcome=%q refund=%v err=%v", outcome, failedRefund != nil, err)
	}
}

func TestStormStateTTLMatchesFullRefillWindow(t *testing.T) {
	tests := []struct {
		name        string
		rate, burst float64
		wantMillis  int64
	}{
		{name: "十秒回满", rate: 1, burst: 10, wantMillis: 10000},
		{name: "亚秒回满至少保留一秒", rate: 100, burst: 1, wantMillis: 1000},
		{name: "小数向上取整", rate: 0.3, burst: 1, wantMillis: 3334},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stormStateTTLMillis(tt.rate, tt.burst)
			if err != nil || got != tt.wantMillis {
				t.Fatalf("TTL=%d want=%d err=%v", got, tt.wantMillis, err)
			}
		})
	}
	if _, err := stormStateTTLMillis(math.SmallestNonzeroFloat64, math.MaxFloat64); err == nil {
		t.Fatal("无法表示的回满时间必须拒绝")
	}
}

func TestRedisStormScopeStoreRejectsInvalidLimit(t *testing.T) {
	store := NewRedisStormScopeStore(nil, "")
	if _, err := store.TryAcquire(context.Background(), "global", 0, 1); err == nil {
		t.Fatal("非法预算必须拒绝")
	}
}
