package rate

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const defaultUpstreamCooldown = 5 * time.Minute
const sessionWindow5hDuration = 5 * time.Hour
const sessionWindow7dDuration = 7 * 24 * time.Hour

var sessionWindow5hStatusHeaders = []string{
	sessionWindow5hPrefix + "-status",
	"x-ratelimit-5h-status",
	"x-codex-5h-status",
}

var sessionWindow5hResetHeaders = []string{
	sessionWindow5hPrefix + "-reset",
	"x-ratelimit-5h-reset",
	"x-codex-5h-reset",
	"x-ratelimit-reset",
}

var sessionWindow7dStatusHeaders = []string{
	sessionWindow7dPrefix + "-status",
	"x-ratelimit-7d-status",
	"x-codex-7d-status",
}

var sessionWindow7dResetHeaders = []string{
	sessionWindow7dPrefix + "-reset",
	"x-ratelimit-7d-reset",
	"x-codex-7d-reset",
}

var sessionWindow5hUtilizationHeaders = []string{
	sessionWindow5hPrefix + "-utilization",
	"x-ratelimit-5h-utilization",
	"x-codex-5h-utilization",
}

var sessionWindow7dUtilizationHeaders = []string{
	sessionWindow7dPrefix + "-utilization",
	"x-ratelimit-7d-utilization",
	"x-codex-7d-utilization",
}

type SessionWindowUpdate struct {
	ProviderAccountID   int64
	Window5hStart       *time.Time
	Window5hEnd         *time.Time
	Window5hStatus      *string
	Window5hUtilization *float64
	Window7dStart       *time.Time
	Window7dEnd         *time.Time
	Window7dStatus      *string
	Window7dUtilization *float64
}

type SessionWindowStore interface {
	UpdateProviderAccountSessionWindows(context.Context, SessionWindowUpdate) error
}

// StatusCooldownSource 为没有 Retry-After/窗口重置头的新事件提供按状态码的默认冷却。
// 返回错误或非正值时，服务回退构造时的现实默认，不让设置后端故障阻断上游失败转移。
type StatusCooldownSource interface {
	CooldownForStatus(context.Context, int) (time.Duration, error)
}

// CooldownStateStore 原子地清除单个账号上的每一个冷却状态列,使一个被下场
// (benched)的上游账号重新变为可调度。它不带 tenant 作用域:ClearCascade 的
// 调用方是系统可信的(或在委派前已自行做过 tenant 归属守卫),因此该实现
// 仅按 account id 寻址。
type CooldownStateStore interface {
	ClearCooldownCascade(ctx context.Context, accountID int64) error
}

type sessionWindowDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type PostgresSessionWindowStore struct {
	db sessionWindowDB
}

type PostgresCooldownStateStore struct {
	db sessionWindowDB
}

type upstreamRateService struct {
	now             func() time.Time
	defaultCooldown time.Duration
	sessionWindows  SessionWindowStore
	cooldownState   CooldownStateStore // nil = 空操作(默认)
	cooldownSource  StatusCooldownSource
	transient       TransientCooldownConfig
	disableCooling  bool
	rulesProvider   AccountErrorRulesProvider // nil = 空操作(默认)
}

type UpstreamRateServiceOption func(*upstreamRateService)

func WithDisableCooling(disabled bool) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.disableCooling = disabled
	}
}

func WithTransientCooldown(duration time.Duration) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.transient = TransientCooldownConfig{Duration: duration}
	}
}

func WithCooldownSource(source StatusCooldownSource) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.cooldownSource = source
	}
}

// WithAccountErrorRulesProvider 注入一个为按账号 temp-unschedulable 规则和
// custom error codes 提供数据的 provider。nil 的 provider(默认)保留零配置的
// 空操作行为。
func WithAccountErrorRulesProvider(p AccountErrorRulesProvider) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.rulesProvider = p
	}
}

// WithCooldownStateStore 注入支撑 ClearCascade 的 store。nil 的 store(默认)
// 保留零配置的空操作行为,因此在生产接入真实 store 之前,ClearCascade 保持
// 为安全的空操作。
func WithCooldownStateStore(store CooldownStateStore) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.cooldownState = store
	}
}

func NewUpstreamRateService(clock func() time.Time, defaultCooldown time.Duration, opts ...UpstreamRateServiceOption) Service {
	return NewUpstreamRateServiceWithSessionWindowStore(clock, defaultCooldown, nil, opts...)
}

func NewUpstreamRateServiceWithSessionWindowStore(clock func() time.Time, defaultCooldown time.Duration, store SessionWindowStore, opts ...UpstreamRateServiceOption) Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if defaultCooldown <= 0 {
		defaultCooldown = defaultUpstreamCooldown
	}
	svc := &upstreamRateService{now: clock, defaultCooldown: defaultCooldown, sessionWindows: store}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

func NewPostgresSessionWindowStore(db sessionWindowDB) *PostgresSessionWindowStore {
	return &PostgresSessionWindowStore{db: db}
}

func (s *PostgresSessionWindowStore) UpdateProviderAccountSessionWindows(ctx context.Context, update SessionWindowUpdate) error {
	if s == nil || s.db == nil || update.ProviderAccountID <= 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
UPDATE provider_accounts
SET session_window_5h_start = COALESCE($2::timestamptz, session_window_5h_start),
    session_window_5h_end = COALESCE($3::timestamptz, session_window_5h_end),
    session_window_5h_status = COALESCE($4::text, session_window_5h_status),
    session_window_5h_utilization = COALESCE($5::numeric, session_window_5h_utilization),
    session_window_7d_start = COALESCE($6::timestamptz, session_window_7d_start),
    session_window_7d_end = COALESCE($7::timestamptz, session_window_7d_end),
    session_window_7d_status = COALESCE($8::text, session_window_7d_status),
    session_window_7d_utilization = COALESCE($9::numeric, session_window_7d_utilization),
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
`, update.ProviderAccountID,
		utcTimePointer(update.Window5hStart), utcTimePointer(update.Window5hEnd), update.Window5hStatus, update.Window5hUtilization,
		utcTimePointer(update.Window7dStart), utcTimePointer(update.Window7dEnd), update.Window7dStatus, update.Window7dUtilization,
	)
	return err
}

func NewPostgresCooldownStateStore(db sessionWindowDB) *PostgresCooldownStateStore {
	return &PostgresCooldownStateStore{db: db}
}

// ClearCooldownCascade 在单条原子 UPDATE 中清除一个账号上的每个冷却状态列:
// rate-limit(3 列)、overload(1 列)、temp-unschedulable(3 列)、
// model_rate_limits jsonb,以及 OpenAI-403 计数窗口(2 列)。这与 tenant 作用域
// 的 admin clear-rate-limit 路径所重置的是同一组列;此处 WHERE 仅按 id(无
// tenant),因为调用方是系统可信的,或已自行强制做过归属校验。清除本就已清空
// 的列是一个安全、幂等的空操作。
func (s *PostgresCooldownStateStore) ClearCooldownCascade(ctx context.Context, accountID int64) error {
	if s == nil || s.db == nil || accountID <= 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
UPDATE provider_accounts
SET rate_limited_at = NULL,
    rate_limit_reset_at = NULL,
    rate_limit_reason = NULL,
    overload_until = NULL,
    temp_unschedulable_until = NULL,
    temp_unschedulable_reason = NULL,
    temp_unschedulable_rule_index = NULL,
    model_rate_limits = '{}'::jsonb,
    openai_403_counter = 0,
    openai_403_window_start = NULL,
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
`, accountID)
	return err
}

func (s *upstreamRateService) HandleUpstreamError(ctx context.Context, accountID int64, statusCode int, respHeaders http.Header, respBody []byte) (Decision, error) {
	now := s.now().UTC()

	// F-RATE-001 §1.6:当该账号上启用了此特性时,评估按账号的
	// temp-unschedulable 规则和 custom error codes。
	// 这是增量式的 —— 先于现有的 429/529 路径评估,这样运营者就能对任意
	// 上游状态码(包括 403)进行分类。
	if s.rulesProvider != nil {
		policy := s.rulesProvider.GetAccountErrorPolicy(accountID)
		if len(policy.Rules) > 0 || len(policy.CustomErrorCodes) > 0 {
			if dec := evalAccountErrorRules(statusCode, respBody, policy.Rules, policy.CustomErrorCodes,
				func(minutes int) time.Duration {
					if minutes <= 0 {
						return s.defaultCooldown
					}
					return time.Duration(minutes) * time.Minute
				},
				s.defaultCooldown,
				now, s.disableCooling,
			); dec.StateChange != StateNoChange {
				return dec, nil
			}
		}
		if policy.PoolMode {
			slog.InfoContext(ctx, "账号池模式跳过未匹配自定义规则的本地状态改写",
				"provider_account_id", accountID,
				"upstream_status_code", statusCode,
			)
			return Decision{ShouldFailover: true, SuppressLocalState: true}, nil
		}
	}

	if statusCode != http.StatusTooManyRequests && statusCode != 529 {
		d, reason, ok := TransientCooldown(statusCode, s.transient)
		if !ok {
			return Decision{}, nil
		}
		dec := Decision{
			StateChange:       StateOverloaded,
			Reason:            reason,
			ShouldFailover:    true,
			RetryAfterSeconds: durationSeconds(d),
		}
		if !s.disableCooling {
			dec.CooldownUntil = now.Add(d).UTC()
		}
		return dec, nil
	}
	until, retryAfterSeconds, ok := retryAfterCooldown(respHeaders, now)
	reason := ReasonRateLimitRPM
	if statusCode == http.StatusTooManyRequests {
		if resetUntil, resetReason, resetOK := ParseMultiWindowReset(respHeaders, now); resetOK {
			until = resetUntil
			retryAfterSeconds = durationSeconds(resetUntil.Sub(now))
			ok = true
			reason = resetReason
		}
	}
	if !ok {
		cooldown := s.defaultCooldownFor(ctx, statusCode)
		retryAfterSeconds = durationSeconds(cooldown)
		until = now.Add(time.Duration(retryAfterSeconds) * time.Second)
	}
	dec := Decision{
		ShouldFailover:    true,
		RetryAfterSeconds: retryAfterSeconds,
	}
	if !s.disableCooling {
		dec.CooldownUntil = until.UTC()
	}
	if statusCode == 529 {
		dec.StateChange = StateOverloaded
		dec.Reason = ReasonOverloaded
		return dec, nil
	}
	dec.StateChange = StateRateLimited
	dec.Reason = reason
	return dec, nil
}

func (s *upstreamRateService) defaultCooldownFor(ctx context.Context, statusCode int) time.Duration {
	if s == nil {
		return defaultUpstreamCooldown
	}
	fallback := s.defaultCooldown
	if fallback <= 0 {
		fallback = defaultUpstreamCooldown
	}
	if s.cooldownSource == nil {
		return fallback
	}
	cooldown, err := s.cooldownSource.CooldownForStatus(ctx, statusCode)
	if err != nil || cooldown <= 0 {
		return fallback
	}
	return cooldown
}

// ClearCascade 遵守 rate.Service 契约(rate.go §ClearCascade):原子地清除
// 一个账号的全部冷却状态。它委托给注入的 CooldownStateStore。nil 的 store
// (零配置默认)使其保持为安全的空操作,因此未接线的 Service 不会报错。
// actorID 是为契约签名和审计对称性而携带的,但 store 只按 id 清除 ——
// tenant 作用域的审计行由 admin HTTP 路径负责。
func (s *upstreamRateService) ClearCascade(ctx context.Context, accountID int64, actorID string) error {
	_ = actorID
	if s == nil || s.cooldownState == nil || accountID <= 0 {
		return nil
	}
	return s.cooldownState.ClearCooldownCascade(ctx, accountID)
}

func (s *upstreamRateService) UpdateSessionWindow(ctx context.Context, accountID int64, headers http.Header) error {
	if s == nil || s.sessionWindows == nil || accountID <= 0 {
		return nil
	}
	now := s.now().UTC()
	update := SessionWindowUpdate{ProviderAccountID: accountID}
	setSessionWindowFromHeaders(
		headers, now, sessionWindow5hStatusHeaders, sessionWindow5hResetHeaders,
		sessionWindow5hUtilizationHeaders, sessionWindow5hDuration,
		&update.Window5hStart, &update.Window5hEnd, &update.Window5hStatus, &update.Window5hUtilization,
	)
	setSessionWindowFromHeaders(
		headers, now, sessionWindow7dStatusHeaders, sessionWindow7dResetHeaders,
		sessionWindow7dUtilizationHeaders, sessionWindow7dDuration,
		&update.Window7dStart, &update.Window7dEnd, &update.Window7dStatus, &update.Window7dUtilization,
	)
	if !update.hasValues() {
		return nil
	}
	return s.sessionWindows.UpdateProviderAccountSessionWindows(ctx, update)
}

func setSessionWindowFromHeaders(
	headers http.Header,
	now time.Time,
	statusHeaders, resetHeaders, utilizationHeaders []string,
	duration time.Duration,
	start, end **time.Time,
	status **string,
	utilization **float64,
) {
	parsedStatus := sessionWindowStatus(headers, statusHeaders)
	if windowEnd, ok := sessionWindowReset(headers, resetHeaders, now, duration); ok && parsedStatus != "" {
		windowStart := windowEnd.Add(-duration).UTC()
		windowEnd = windowEnd.UTC()
		*start = &windowStart
		*end = &windowEnd
		*status = &parsedStatus
	}
	*utilization = sessionWindowUtilization(headers, utilizationHeaders)
}

func sessionWindowStatus(headers http.Header, names []string) string {
	if headers == nil {
		return ""
	}
	for _, name := range names {
		status := strings.TrimSpace(headers.Get(name))
		if status == "" {
			continue
		}
		if len(status) > 64 {
			status = status[:64]
		}
		return status
	}
	return ""
}

func sessionWindowReset(headers http.Header, names []string, now time.Time, duration time.Duration) (time.Time, bool) {
	if headers == nil {
		return time.Time{}, false
	}
	var raw string
	for _, name := range names {
		raw = strings.TrimSpace(headers.Get(name))
		if raw != "" {
			break
		}
	}
	if raw == "" {
		return time.Time{}, false
	}
	resetUnix, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || resetUnix <= 0 {
		return time.Time{}, false
	}
	if resetUnix > 100_000_000_000 {
		resetUnix = resetUnix / 1000
	}
	resetAt := time.Unix(resetUnix, 0).UTC()
	if resetAt.Before(now.Add(-duration)) || resetAt.After(now.Add(sessionWindow7dDuration)) {
		return time.Time{}, false
	}
	return resetAt, true
}

func sessionWindowUtilization(headers http.Header, names []string) *float64 {
	if headers == nil {
		return nil
	}
	for _, name := range names {
		raw := strings.TrimSpace(headers.Get(name))
		if raw == "" {
			continue
		}
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return nil
		}
		return &value
	}
	return nil
}

func (u SessionWindowUpdate) hasValues() bool {
	return u.Window5hStart != nil || u.Window5hEnd != nil || u.Window5hStatus != nil || u.Window5hUtilization != nil ||
		u.Window7dStart != nil || u.Window7dEnd != nil || u.Window7dStatus != nil || u.Window7dUtilization != nil
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func retryAfterCooldown(headers http.Header, now time.Time) (time.Time, int, bool) {
	if headers == nil {
		return time.Time{}, 0, false
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return time.Time{}, 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return time.Time{}, 0, false
		}
		return now.Add(time.Duration(seconds) * time.Second).UTC(), seconds, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, 0, false
	}
	delta := when.Sub(now)
	if delta <= 0 {
		return time.Time{}, 0, false
	}
	return when.UTC(), durationSeconds(delta), true
}

func durationSeconds(d time.Duration) int {
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

var _ Service = (*upstreamRateService)(nil)
