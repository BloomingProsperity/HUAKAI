package workerlease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// WindowClaimer 保证一个逻辑任务在同一时间窗口内最多被一个副本认领。
type WindowClaimer interface {
	TryClaim(context.Context, time.Duration) (bool, error)
}

// WindowClaimFactory 为不同组件和作用域创建隔离的窗口认领器。
type WindowClaimFactory interface {
	For(component, scope string) WindowClaimer
}

type RedisWindowClaims struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisWindowClaims(client redis.UniversalClient, prefix string) *RedisWindowClaims {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "huakai:worker-window"
	}
	return &RedisWindowClaims{client: client, prefix: prefix}
}

func (r *RedisWindowClaims) For(component, scope string) WindowClaimer {
	component = normalizeWindowComponent(component)
	if component == "" || r == nil || r.client == nil {
		return invalidWindowClaimer{}
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(scope)))
	baseKey := r.prefix + ":" + component + ":" + hex.EncodeToString(digest[:])
	return &redisWindowClaimer{client: r.client, baseKey: baseKey}
}

type redisWindowClaimer struct {
	client  redis.UniversalClient
	baseKey string
}

func (c *redisWindowClaimer) TryClaim(ctx context.Context, window time.Duration) (bool, error) {
	if c == nil || c.client == nil || c.baseKey == "" || window <= 0 {
		return false, errors.New("workerlease: invalid window claim")
	}
	result, err := windowClaimScript.Run(ctx, c.client, []string{c.baseKey}, window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("workerlease: claim time window: %w", err)
	}
	return result == 1, nil
}

type invalidWindowClaimer struct{}

func (invalidWindowClaimer) TryClaim(context.Context, time.Duration) (bool, error) {
	return false, errors.New("workerlease: invalid window claim")
}

func normalizeWindowComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return ""
		}
	}
	return value
}

var windowClaimScript = redis.NewScript(`
local window = tonumber(ARGV[1])
if not window or window <= 0 then
  return redis.error_reply("invalid claim window")
end
local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local bucket = math.floor(now / window)
local expires = ((bucket + 1) * window) - now
if expires < 1 then
  expires = 1
end
local key = KEYS[1] .. ":" .. bucket
local acquired = redis.call("SET", key, "1", "PX", expires, "NX")
if acquired then
  return 1
end
return 0
`)
