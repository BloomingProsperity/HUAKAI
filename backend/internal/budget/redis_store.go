package budget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisReservationTTL = 24 * time.Hour

var checkAndIncrementScript = redis.NewScript(`
local tm = redis.call("TIME")
local now = tonumber(tm[1])
local minute = math.floor(now / 60)
local prefix = ARGV[1]
local rpm_limit = tonumber(ARGV[2])
local tpm_limit = tonumber(ARGV[3])
local rpm_inc = tonumber(ARGV[4])
local tpm_inc = tonumber(ARGV[5])
local claim = ARGV[6]
local rkey = prefix .. "r:" .. minute
local tkey = prefix .. "t:" .. minute
local ckey = prefix .. "c:" .. minute
if redis.call("SISMEMBER", ckey, claim) == 1 then
  return {1, "idempotent", minute, 0, 0, 0}
end
local rcur = tonumber(redis.call("GET", rkey) or "0")
local tcur = tonumber(redis.call("GET", tkey) or "0")
if rpm_limit > 0 and rcur + rpm_inc > rpm_limit then
  return {0, "rpm", minute, rcur, rpm_limit, 60 - (now % 60)}
end
if tpm_limit > 0 and tcur + tpm_inc > tpm_limit then
  return {0, "tpm", minute, tcur, tpm_limit, 60 - (now % 60)}
end
redis.call("INCRBY", rkey, rpm_inc)
redis.call("INCRBY", tkey, tpm_inc)
redis.call("SADD", ckey, claim)
redis.call("EXPIRE", rkey, 120)
redis.call("EXPIRE", tkey, 120)
redis.call("EXPIRE", ckey, 120)
return {1, "allow", minute, rcur + rpm_inc, tcur + tpm_inc, 0}
`)

var adjustScript = redis.NewScript(`
local prefix = ARGV[1]
local minute = ARGV[2]
local claim = ARGV[3]
local rpm_delta = tonumber(ARGV[4])
local tpm_delta = tonumber(ARGV[5])
local remove = tonumber(ARGV[6])
local rkey = prefix .. "r:" .. minute
local tkey = prefix .. "t:" .. minute
local ckey = prefix .. "c:" .. minute
if rpm_delta ~= 0 then
  local v = redis.call("INCRBY", rkey, rpm_delta)
  if v < 0 then redis.call("SET", rkey, 0) end
  redis.call("EXPIRE", rkey, 120)
end
if tpm_delta ~= 0 then
  local v = redis.call("INCRBY", tkey, tpm_delta)
  if v < 0 then redis.call("SET", tkey, 0) end
  redis.call("EXPIRE", tkey, 120)
end
if remove == 1 then
  redis.call("SREM", ckey, claim)
end
redis.call("EXPIRE", ckey, 120)
return {1}
`)

type RedisStore struct {
	client    *redis.Client
	once      sync.Once
	preloadMu sync.Mutex
	shaErr    error
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) CheckAndIncrement(ctx context.Context, req CounterRequest) (CounterResult, error) {
	if s == nil || s.client == nil {
		return CounterResult{}, ErrUnavailable
	}
	prefix, err := redisScopePrefix(req.Scope)
	if err != nil {
		return CounterResult{}, err
	}
	if err := s.preload(ctx); err != nil {
		return CounterResult{}, err
	}
	anchor := prefix + "anchor"
	raw, err := checkAndIncrementScript.Run(ctx, s.client, []string{anchor},
		prefix,
		req.Limits.normalized().RPM,
		req.Limits.normalized().TPM,
		nonNegative(req.RPMIncrement),
		nonNegative(req.TPMIncrement),
		intString(req.ClaimID),
	).Result()
	if err != nil {
		return CounterResult{}, err
	}
	return parseCounterScriptResult(raw)
}

func (s *RedisStore) Adjust(ctx context.Context, req AdjustRequest) error {
	if s == nil || s.client == nil {
		return ErrUnavailable
	}
	prefix, err := redisScopePrefix(req.Scope)
	if err != nil {
		return err
	}
	if err := s.preload(ctx); err != nil {
		return err
	}
	remove := 0
	if req.Remove {
		remove = 1
	}
	anchor := prefix + "anchor"
	_, err = adjustScript.Run(ctx, s.client, []string{anchor},
		prefix,
		req.Minute,
		intString(req.ClaimID),
		req.RPMDelta,
		req.TPMDelta,
		remove,
	).Result()
	return err
}

func (s *RedisStore) LoadReservation(ctx context.Context, tenantID, claimID int64) (StoredReservation, bool, error) {
	if s == nil || s.client == nil {
		return StoredReservation{}, false, ErrUnavailable
	}
	raw, err := s.client.Get(ctx, redisReservationKey(tenantID, claimID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return StoredReservation{}, false, nil
	}
	if err != nil {
		return StoredReservation{}, false, err
	}
	var res StoredReservation
	if err := json.Unmarshal(raw, &res); err != nil {
		return StoredReservation{}, false, err
	}
	return res, true, nil
}

func (s *RedisStore) SaveReservation(ctx context.Context, res StoredReservation) error {
	if s == nil || s.client == nil {
		return ErrUnavailable
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, redisReservationKey(res.TenantID, res.ClaimID), raw, redisReservationTTL).Err()
}

func (s *RedisStore) UpdateReservation(ctx context.Context, res StoredReservation) error {
	return s.SaveReservation(ctx, res)
}

func (s *RedisStore) preload(ctx context.Context) error {
	s.once.Do(func() {
		s.shaErr = s.loadScripts(ctx)
	})
	if s.shaErr != nil {
		s.preloadMu.Lock()
		defer s.preloadMu.Unlock()
		if s.shaErr != nil {
			s.shaErr = s.loadScripts(ctx)
		}
	}
	return s.shaErr
}

func (s *RedisStore) loadScripts(ctx context.Context) error {
	if s.client == nil {
		return ErrUnavailable
	}
	if err := checkAndIncrementScript.Load(ctx, s.client).Err(); err != nil {
		return err
	}
	return adjustScript.Load(ctx, s.client).Err()
}

func parseCounterScriptResult(raw any) (CounterResult, error) {
	items, ok := raw.([]any)
	if !ok {
		return CounterResult{}, fmt.Errorf("budget redis: unexpected script result %T", raw)
	}
	if len(items) < 6 {
		return CounterResult{}, fmt.Errorf("budget redis: short script result")
	}
	allowed := redisInt(items[0]) == 1
	reason := redisString(items[1])
	minute := redisInt(items[2])
	current := redisInt(items[3])
	limit := redisInt(items[4])
	retry := redisInt(items[5])
	out := CounterResult{Allowed: allowed, Minute: minute, Current: current, Limit: limit}
	if reason == "idempotent" {
		out.Allowed = true
		out.IdempotencyHit = true
	}
	if !allowed {
		if reason == "tpm" {
			out.Counter = CounterTPM
		} else {
			out.Counter = CounterRPM
		}
		out.RetryAfter = time.Duration(retry) * time.Second
		if out.RetryAfter <= 0 {
			out.RetryAfter = time.Second
		}
	}
	return out, nil
}

func redisInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	case []byte:
		n, _ := strconv.ParseInt(string(x), 10, 64)
		return n
	default:
		return 0
	}
}

func redisString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

func IsRedisClosed(err error) bool {
	return errors.Is(err, redis.ErrClosed)
}

func redisReservationKey(tenantID, claimID int64) string {
	return "bgtclaim:" + intString(tenantID) + ":" + intString(claimID)
}
