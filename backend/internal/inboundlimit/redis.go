// Package inboundlimit 提供公网入站限流的跨实例原子计数后端。
package inboundlimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrInvalidLimit = errors.New("inboundlimit: invalid limit")

// Limit 是一个令牌桶合同。RatePerSecond 与 Burst 都必须为正数。
type Limit struct {
	RatePerSecond float64
	Burst         float64
}

// Decision 是共享后端对一次请求的裁决。
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Store 是 HTTP 中间件所需的窄接口。
type Store interface {
	Allow(context.Context, string, string, Limit) (Decision, error)
}

// RedisStore 使用 Redis 服务端时间和 Lua 原子执行补充、消费与过期。
type RedisStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisStore(client redis.UniversalClient, prefix string) *RedisStore {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "huakai:inbound-limit"
	}
	return &RedisStore{client: client, prefix: prefix}
}

func (s *RedisStore) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("inboundlimit: redis client is required")
	}
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) Allow(ctx context.Context, tier, subject string, limit Limit) (Decision, error) {
	if s == nil || s.client == nil {
		return Decision{}, errors.New("inboundlimit: redis client is required")
	}
	tier = normalizeTier(tier)
	if tier == "" || strings.TrimSpace(subject) == "" || !validLimit(limit) {
		return Decision{}, ErrInvalidLimit
	}
	key := s.redisKey(tier, subject)
	ratePerMillisecond := limit.RatePerSecond / 1000
	ttl := bucketTTL(limit)
	result, err := tokenBucketScript.Run(ctx, s.client, []string{key},
		ratePerMillisecond,
		limit.Burst,
		ttl.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("执行共享入站限流: %w", err)
	}
	if len(result) != 2 {
		return Decision{}, errors.New("inboundlimit: unexpected redis result")
	}
	retry := time.Duration(result[1]) * time.Millisecond
	if result[0] == 0 && retry < time.Second {
		retry = time.Second
	}
	return Decision{Allowed: result[0] == 1, RetryAfter: retry}, nil
}

func (s *RedisStore) redisKey(tier, subject string) string {
	digest := sha256.Sum256([]byte(subject))
	return s.prefix + ":" + tier + ":" + hex.EncodeToString(digest[:])
}

func validLimit(limit Limit) bool {
	return limit.RatePerSecond > 0 && !math.IsNaN(limit.RatePerSecond) && !math.IsInf(limit.RatePerSecond, 0) &&
		limit.Burst >= 1 && !math.IsNaN(limit.Burst) && !math.IsInf(limit.Burst, 0)
}

func normalizeTier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return ""
		}
	}
	return value
}

func bucketTTL(limit Limit) time.Duration {
	seconds := math.Ceil((limit.Burst / limit.RatePerSecond) * 2)
	if seconds < 60 {
		seconds = 60
	}
	if seconds > 24*60*60 {
		seconds = 24 * 60 * 60
	}
	return time.Duration(seconds) * time.Second
}

var tokenBucketScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
if not rate or rate <= 0 or not burst or burst < 1 or not ttl or ttl <= 0 then
  return redis.error_reply("invalid token bucket arguments")
end

local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local state = redis.call("HMGET", KEYS[1], "tokens", "updated_at")
local tokens = tonumber(state[1])
local updated = tonumber(state[2])
if not tokens or not updated then
  tokens = burst
  updated = now
end
if updated > now then
  updated = now
end
tokens = math.min(burst, tokens + ((now - updated) * rate))

local allowed = 0
local retry_after = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry_after = math.ceil((1 - tokens) / rate)
end

redis.call("HSET", KEYS[1], "tokens", tokens, "updated_at", now)
redis.call("PEXPIRE", KEYS[1], ttl)
return {allowed, retry_after}
`)
