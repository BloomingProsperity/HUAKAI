package loginthrottle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// SharedReservation 是一次已落到共享后端的登录计算槽。
type SharedReservation interface {
	Success(context.Context) error
	Failure(context.Context) error
	Cancel(context.Context) error
}

// SharedStore 负责跨副本原子预留登录计算槽和提交结果。
type SharedStore interface {
	Reserve(context.Context, string, Config) (SharedReservation, Decision, error)
}

type RedisStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisStore(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client, prefix: "huakai:login-throttle"}
}

func (s *RedisStore) Reserve(ctx context.Context, subject string, cfg Config) (SharedReservation, Decision, error) {
	if s == nil || s.client == nil {
		return nil, Decision{}, errors.New("loginthrottle: redis client is required")
	}
	if subject == "" {
		subject = "unknown"
	}
	reservationID, err := newReservationID()
	if err != nil {
		return nil, Decision{}, fmt.Errorf("loginthrottle: create reservation id: %w", err)
	}
	keys := s.keys(subject)
	result, err := loginReserveScript.Run(ctx, s.client, keys,
		cfg.InFlightLimit,
		cfg.Window.Milliseconds(),
		cfg.WindowLimit,
		cfg.BanWindow.Milliseconds(),
		cfg.InFlightTTL.Milliseconds(),
		sharedStateTTL(cfg).Milliseconds(),
		reservationID,
	).Int64Slice()
	if err != nil {
		return nil, Decision{}, fmt.Errorf("loginthrottle: reserve shared slot: %w", err)
	}
	if len(result) != 3 {
		return nil, Decision{}, errors.New("loginthrottle: unexpected reserve result")
	}
	reason := reasonFromSharedCode(result[1])
	retry := time.Duration(result[2]) * time.Millisecond
	if result[0] == 0 {
		if retry < time.Second {
			retry = time.Second
		}
		return nil, Decision{Allowed: false, Reason: reason, RetryAfter: retry}, nil
	}
	return &redisReservation{
		client:      s.client,
		keys:        keys,
		id:          reservationID,
		banWindow:   cfg.BanWindow,
		banAfter:    cfg.BanAfter,
		banDuration: cfg.BanDuration,
		stateTTL:    sharedStateTTL(cfg),
	}, Decision{Allowed: true, Reason: ReasonAllowed}, nil
}

func (s *RedisStore) keys(subject string) []string {
	digest := sha256.Sum256([]byte(subject))
	tag := hex.EncodeToString(digest[:])
	base := s.prefix + ":{" + tag + "}"
	return []string{base + ":inflight", base + ":failures", base + ":ban"}
}

type redisReservation struct {
	client      redis.UniversalClient
	keys        []string
	id          string
	banWindow   time.Duration
	banAfter    int
	banDuration time.Duration
	stateTTL    time.Duration
}

func (r *redisReservation) Success(ctx context.Context) error { return r.release(ctx) }
func (r *redisReservation) Cancel(ctx context.Context) error  { return r.release(ctx) }

func (r *redisReservation) release(ctx context.Context) error {
	if r == nil || r.client == nil || len(r.keys) != 3 || r.id == "" {
		return nil
	}
	if err := loginReleaseScript.Run(ctx, r.client, r.keys[:1], r.id).Err(); err != nil {
		return fmt.Errorf("loginthrottle: release shared slot: %w", err)
	}
	return nil
}

func (r *redisReservation) Failure(ctx context.Context) error {
	if r == nil || r.client == nil || len(r.keys) != 3 || r.id == "" {
		return nil
	}
	if err := loginFailureScript.Run(ctx, r.client, r.keys,
		r.id,
		r.banWindow.Milliseconds(),
		r.banAfter,
		r.banDuration.Milliseconds(),
		r.stateTTL.Milliseconds(),
	).Err(); err != nil {
		return fmt.Errorf("loginthrottle: commit shared failure: %w", err)
	}
	return nil
}

func newReservationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func sharedStateTTL(cfg Config) time.Duration {
	keep := cfg.Window
	if cfg.BanWindow > keep {
		keep = cfg.BanWindow
	}
	if cfg.InFlightTTL > keep {
		keep = cfg.InFlightTTL
	}
	keep += cfg.BanDuration
	if keep < time.Minute {
		keep = time.Minute
	}
	if keep > 30*24*time.Hour {
		keep = 30 * 24 * time.Hour
	}
	return keep
}

func reasonFromSharedCode(code int64) Reason {
	switch code {
	case 0:
		return ReasonAllowed
	case 1:
		return ReasonIPInFlight
	case 2:
		return ReasonIPWindow
	case 3:
		return ReasonIPBanned
	default:
		return ReasonBackendUnavailable
	}
}

var loginReserveScript = redis.NewScript(`
local inflight_limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local window_limit = tonumber(ARGV[3])
local ban_window_ms = tonumber(ARGV[4])
local inflight_ttl_ms = tonumber(ARGV[5])
local state_ttl_ms = tonumber(ARGV[6])
local reservation_id = ARGV[7]
if not inflight_limit or inflight_limit <= 0 or not window_ms or window_ms <= 0 or
   not window_limit or window_limit <= 0 or not ban_window_ms or ban_window_ms <= 0 or
   not inflight_ttl_ms or inflight_ttl_ms <= 0 or not state_ttl_ms or state_ttl_ms <= 0 or
   not reservation_id or reservation_id == "" then
  return redis.error_reply("invalid login throttle arguments")
end

local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local history_window = math.max(window_ms, ban_window_ms)
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now)
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now - history_window)

local banned_until = tonumber(redis.call("GET", KEYS[3])) or 0
if banned_until > now then
  return {0, 3, banned_until - now}
end

local inflight = redis.call("ZCARD", KEYS[1])
if inflight >= inflight_limit then
  return {0, 1, 1000}
end
local failures = redis.call("ZCOUNT", KEYS[2], now - window_ms + 1, "+inf")
if failures + inflight >= window_limit then
  return {0, 2, window_ms}
end

redis.call("ZADD", KEYS[1], now + inflight_ttl_ms, reservation_id)
redis.call("PEXPIRE", KEYS[1], state_ttl_ms)
redis.call("PEXPIRE", KEYS[2], state_ttl_ms)
return {1, 0, 0}
`)

var loginReleaseScript = redis.NewScript(`
redis.call("ZREM", KEYS[1], ARGV[1])
return 1
`)

var loginFailureScript = redis.NewScript(`
local reservation_id = ARGV[1]
local ban_window_ms = tonumber(ARGV[2])
local ban_after = tonumber(ARGV[3])
local ban_duration_ms = tonumber(ARGV[4])
local state_ttl_ms = tonumber(ARGV[5])
local removed = redis.call("ZREM", KEYS[1], reservation_id)
if removed == 0 then
  return 0
end
local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now - ban_window_ms)
redis.call("ZADD", KEYS[2], now, reservation_id)
local failures = redis.call("ZCOUNT", KEYS[2], now - ban_window_ms + 1, "+inf")
if failures >= ban_after then
  local banned_until = now + ban_duration_ms
  redis.call("SET", KEYS[3], banned_until, "PX", ban_duration_ms)
end
redis.call("PEXPIRE", KEYS[1], state_ttl_ms)
redis.call("PEXPIRE", KEYS[2], state_ttl_ms)
return 1
`)
