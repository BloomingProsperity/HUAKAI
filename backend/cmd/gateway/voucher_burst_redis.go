package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

// redisVoucherBurstStore 把 go-redis 客户端适配成 voucher.burstCounterStore:GET 读计数、INCR+(首次)EXPIRE 增计数。
// 让 voucher 包不直接依赖 go-redis、且可脱离真实 Redis 单测(测试用内存替身)。
type redisVoucherBurstStore struct {
	client *redis.Client
}

func (s redisVoucherBurstStore) Count(ctx context.Context, key string) (int64, error) {
	n, err := s.client.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil // key 不存在 = 计数 0
	}
	return n, err
}

func (s redisVoucherBurstStore) IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	n, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if n == 1 {
		// 仅首次创建该窗口时设过期,使窗口在 ttl 后自动重置(不在每次失败时续期)。
		_ = s.client.Expire(ctx, key, ttl).Err()
	}
	return n, nil
}

// buildVoucherServiceOptions 组装 voucher.NewService 的 options:审计 sink 必带;配置了 Redis 时再叠加
// 跨实例 RedisBurstLimiter(否则不叠加,NewService 默认用单进程内存限流器)。
func buildVoucherServiceOptions(cfg *Config) []voucher.Option {
	opts := []voucher.Option{voucher.WithAuditSink(voucher.PrivacyLogAuditSink{})}
	if limiter := buildVoucherBurstLimiter(cfg); limiter != nil {
		opts = append(opts, voucher.WithBurstLimiter(limiter))
	}
	return opts
}

// buildVoucherBurstLimiter 在配置了 Redis 时返回跨实例的 RedisBurstLimiter(复用 budget 的 Redis 实例);
// 未配置 / URL 非法时返回 nil —— 调用方据此不传 WithBurstLimiter,voucher.NewService 退回单进程内存限流器。
func buildVoucherBurstLimiter(cfg *Config) voucher.BurstLimiter {
	if cfg == nil || strings.TrimSpace(cfg.Budget.RedisURL) == "" {
		return nil
	}
	opts, err := redis.ParseURL(cfg.Budget.RedisURL)
	if err != nil {
		// URL 非法 → 退回单进程内存限流器(多副本下每副本独立计数 = 限额×副本数,等于变相放开)。
		// 与 budget 侧同款响亮告警,避免将来两者 URL 解耦后这里静默退化、运维无感。
		_ = privacy.LogSystem(context.Background(), privacy.SystemEvent{
			Severity:   privacy.SeverityError,
			Component:  "voucher.redis_config",
			ErrorClass: privacy.ErrorClassFor(context.Background(), err),
			Attrs:      map[string]any{"event_class": "voucher_redis_url_invalid_fallback_to_memory"},
		})
		return nil
	}
	return voucher.NewRedisBurstLimiter(
		redisVoucherBurstStore{client: redis.NewClient(opts)},
		voucher.DefaultBurstPolicy(),
	)
}
