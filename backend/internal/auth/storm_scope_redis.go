package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/redis/go-redis/v9"
)

// StormScopeStore 为厂商端点和全局刷新预算提供跨实例原子令牌桶。
type StormScopeStore interface {
	TryAcquire(ctx context.Context, key string, rate, burst float64) (bool, error)
	Refund(ctx context.Context, key string, rate, burst float64) error
}

type RedisStormScopeStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisStormScopeStore(client redis.UniversalClient, prefix string) *RedisStormScopeStore {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "huakai:credential-storm"
	}
	return &RedisStormScopeStore{client: client, prefix: prefix}
}

func (s *RedisStormScopeStore) TryAcquire(ctx context.Context, key string, rate, burst float64) (bool, error) {
	redisKey, err := s.redisKey(key, rate, burst)
	if err != nil {
		return false, err
	}
	ttlMillis, err := stormStateTTLMillis(rate, burst)
	if err != nil {
		return false, err
	}
	result, err := stormAcquireScript.Run(ctx, s.client, []string{redisKey}, rate, burst, ttlMillis).Int64()
	if err != nil {
		return false, fmt.Errorf("auth: acquire shared storm token: %w", err)
	}
	return result == 1, nil
}

func (s *RedisStormScopeStore) Refund(ctx context.Context, key string, rate, burst float64) error {
	redisKey, err := s.redisKey(key, rate, burst)
	if err != nil {
		return err
	}
	ttlMillis, err := stormStateTTLMillis(rate, burst)
	if err != nil {
		return err
	}
	if err := stormRefundScript.Run(ctx, s.client, []string{redisKey}, rate, burst, ttlMillis).Err(); err != nil {
		return fmt.Errorf("auth: refund shared storm token: %w", err)
	}
	return nil
}

func (s *RedisStormScopeStore) redisKey(key string, rate, burst float64) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("auth: shared storm store unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" || rate <= 0 || burst < 1 || math.IsInf(rate, 0) || math.IsNaN(rate) || math.IsInf(burst, 0) || math.IsNaN(burst) {
		return "", errors.New("auth: invalid shared storm limit")
	}
	digest := sha256.Sum256([]byte(key))
	return s.prefix + ":" + hex.EncodeToString(digest[:]), nil
}

func stormStateTTLMillis(rate, burst float64) (int64, error) {
	if rate <= 0 || burst < 1 || math.IsInf(rate, 0) || math.IsNaN(rate) || math.IsInf(burst, 0) || math.IsNaN(burst) {
		return 0, errors.New("auth: invalid shared storm limit")
	}
	ttlMillis := math.Ceil((burst / rate) * 1000)
	if math.IsInf(ttlMillis, 0) || math.IsNaN(ttlMillis) || ttlMillis >= float64(math.MaxInt64) {
		return 0, errors.New("auth: shared storm limit refill interval is too large")
	}
	if ttlMillis < 1000 {
		ttlMillis = 1000
	}
	return int64(ttlMillis), nil
}

var stormAcquireScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
if not rate or rate <= 0 or not burst or burst < 1 or not ttl_ms or ttl_ms < 1 then
  return redis.error_reply("invalid storm limit")
end
local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local state = redis.call("HMGET", KEYS[1], "tokens", "updated_at")
local tokens = tonumber(state[1])
local updated = tonumber(state[2])
if not tokens or not updated then
  tokens = burst
  updated = now
elseif now > updated then
  tokens = math.min(burst, tokens + ((now - updated) / 1000) * rate)
end
local acquired = 0
if tokens >= 1 then
  tokens = tokens - 1
  acquired = 1
end
redis.call("HSET", KEYS[1], "tokens", tokens, "updated_at", now)
redis.call("PEXPIRE", KEYS[1], ttl_ms)
return acquired
`)

var stormRefundScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
if not rate or rate <= 0 or not burst or burst < 1 or not ttl_ms or ttl_ms < 1 then
  return redis.error_reply("invalid storm limit")
end
local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local state = redis.call("HMGET", KEYS[1], "tokens", "updated_at")
local tokens = tonumber(state[1])
local updated = tonumber(state[2])
if not tokens or not updated then
  tokens = burst
elseif now > updated then
  tokens = math.min(burst, tokens + ((now - updated) / 1000) * rate)
end
tokens = math.min(burst, tokens + 1)
redis.call("HSET", KEYS[1], "tokens", tokens, "updated_at", now)
redis.call("PEXPIRE", KEYS[1], ttl_ms)
return 1
`)
