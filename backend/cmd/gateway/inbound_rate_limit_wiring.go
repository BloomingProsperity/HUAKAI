package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/inboundlimit"
	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

type sharedRateLimitRuntime struct {
	inbound inboundlimit.Store
	login   loginthrottle.SharedStore
	windows workerlease.WindowClaimFactory
	storm   auth.StormScopeStore
	ping    func(context.Context) error
	close   func() error
}

// buildSharedRateLimits 构造公网共享限流和定时任务认领后端。生产模式必须使用 Redis，
// 避免每增加一个副本就把未认证入口的攻击预算放大一倍；开发模式未配置时
// 返回 nil，由现有进程内令牌桶承担单实例保护。
func buildSharedRateLimits(ctx context.Context, cfg *Config) (sharedRateLimitRuntime, error) {
	if cfg == nil {
		return sharedRateLimitRuntime{}, fmt.Errorf("共享入站限流缺少运行配置")
	}
	if releaseModeProduction() && rateLimitDisabled() {
		return sharedRateLimitRuntime{}, fmt.Errorf("production 模式禁止 HUAKAI_RL_DISABLE，公网准入限流不得关闭")
	}
	rawURL := strings.TrimSpace(cfg.RateLimitRedisURL)
	if rawURL == "" {
		if releaseModeProduction() {
			return sharedRateLimitRuntime{}, fmt.Errorf("production 模式要求 HUAKAI_RATE_LIMIT_REDIS_URL 或 HUAKAI_REDIS_URL，用于跨实例公网限流")
		}
		return sharedRateLimitRuntime{}, nil
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return sharedRateLimitRuntime{}, fmt.Errorf("解析共享限流 Redis URL: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return sharedRateLimitRuntime{}, fmt.Errorf("连接共享限流 Redis: %w", err)
	}
	return sharedRateLimitRuntime{
		inbound: inboundlimit.NewRedisStore(client, ""),
		login:   loginthrottle.NewRedisStore(client),
		windows: workerlease.NewRedisWindowClaims(client, ""),
		storm:   auth.NewRedisStormScopeStore(client, ""),
		ping:    func(checkCtx context.Context) error { return client.Ping(checkCtx).Err() },
		close:   client.Close,
	}, nil
}
