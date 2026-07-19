package workerlease

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisWindowClaimsRejectsInvalidComponent(t *testing.T) {
	factory := NewRedisWindowClaims(nil, "")
	if _, err := factory.For("bad:component", "scope").TryClaim(context.Background(), time.Minute); err == nil {
		t.Fatal("非法组件名必须拒绝")
	}
}

func TestRedisWindowClaimsIntegration(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("HUAKAI_REDIS_URL"))
	if rawURL == "" {
		t.Skip("未配置 HUAKAI_REDIS_URL，跳过 Redis 时间窗认领集成测试")
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("解析 Redis URL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	ctx := context.Background()
	factory := NewRedisWindowClaims(client, "huakai:test:worker-window:"+time.Now().UTC().Format("150405.000000000"))
	first := factory.For("inspection", "tenant:1")
	second := factory.For("inspection", "tenant:1")
	other := factory.For("inspection", "tenant:2")

	acquired, err := first.TryClaim(ctx, time.Hour)
	if err != nil || !acquired {
		t.Fatalf("首次认领 acquired=%v err=%v", acquired, err)
	}
	acquired, err = second.TryClaim(ctx, time.Hour)
	if err != nil || acquired {
		t.Fatalf("同作用域同窗口不得重复认领 acquired=%v err=%v", acquired, err)
	}
	acquired, err = other.TryClaim(ctx, time.Hour)
	if err != nil || !acquired {
		t.Fatalf("不同作用域不得串窗 acquired=%v err=%v", acquired, err)
	}
}
