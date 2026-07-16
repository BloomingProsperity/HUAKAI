package channelhealth

import (
	"context"
	"time"
)

// StatusCooldownSource 为新产生的 429/529 健康事件提供运行时冷却值。
// 读取失败时 Service 回退 Policy 的现实默认，既不阻塞健康写入，也不改存量 CooldownUntil。
type StatusCooldownSource interface {
	CooldownForStatus(context.Context, int) (time.Duration, error)
}

func WithCooldownSource(source StatusCooldownSource) ServiceOption {
	return func(s *Service) {
		s.cooldowns = source
	}
}

func (s *Service) statusCooldown(ctx context.Context, statusCode int, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = DefaultPolicy().DefaultRateLimitCooldown
	}
	if s == nil || s.cooldowns == nil {
		return fallback
	}
	cooldown, err := s.cooldowns.CooldownForStatus(ctx, statusCode)
	if err != nil || cooldown <= 0 {
		return fallback
	}
	return cooldown
}
