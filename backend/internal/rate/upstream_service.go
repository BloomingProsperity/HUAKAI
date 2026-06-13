package rate

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const defaultUpstreamCooldown = 5 * time.Minute
const sessionWindow5hDuration = 5 * time.Hour

var sessionWindow5hStatusHeaders = []string{
	"anthropic-ratelimit-unified-5h-status",
	"x-ratelimit-5h-status",
	"x-codex-5h-status",
}

var sessionWindow5hResetHeaders = []string{
	"anthropic-ratelimit-unified-5h-reset",
	"x-ratelimit-5h-reset",
	"x-codex-5h-reset",
	"x-ratelimit-reset",
}

type SessionWindowUpdate struct {
	ProviderAccountID int64
	WindowStart       time.Time
	WindowEnd         time.Time
	Status            string
}

type SessionWindowStore interface {
	UpdateProviderAccountSessionWindow5h(context.Context, SessionWindowUpdate) error
}

// CooldownStateStore atomically clears every cooldown-state column on a single
// account so a benched upstream account becomes schedulable again. It carries
// no tenant scope: ClearCascade callers are system-trusted (or have already
// performed their own tenant ownership guard before delegating), so the impl
// keys solely on account id.
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
	cooldownState   CooldownStateStore // nil = no-op (default)
	transient       TransientCooldownConfig
	disableCooling  bool
	rulesProvider   AccountErrorRulesProvider // nil = no-op (default)
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

// WithAccountErrorRulesProvider injects a provider for per-account
// temp-unschedulable rules and custom error codes. A nil provider (the
// default) preserves zero-config no-op behaviour.
func WithAccountErrorRulesProvider(p AccountErrorRulesProvider) UpstreamRateServiceOption {
	return func(s *upstreamRateService) {
		s.rulesProvider = p
	}
}

// WithCooldownStateStore injects the store that backs ClearCascade. A nil
// store (the default) preserves zero-config no-op behaviour, so ClearCascade
// stays a safe no-op until production wires a real store.
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

func (s *PostgresSessionWindowStore) UpdateProviderAccountSessionWindow5h(ctx context.Context, update SessionWindowUpdate) error {
	if s == nil || s.db == nil || update.ProviderAccountID <= 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
UPDATE provider_accounts
SET session_window_5h_start = $2,
    session_window_5h_end = $3,
    session_window_5h_status = $4,
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
`, update.ProviderAccountID, update.WindowStart.UTC(), update.WindowEnd.UTC(), update.Status)
	return err
}

func NewPostgresCooldownStateStore(db sessionWindowDB) *PostgresCooldownStateStore {
	return &PostgresCooldownStateStore{db: db}
}

// ClearCooldownCascade clears every cooldown-state column on one account in a
// single atomic UPDATE: rate-limit (3 cols), overload (1), temp-unschedulable
// (3), model_rate_limits jsonb, and the OpenAI-403 counter window (2). This is
// the same column set the tenant-scoped admin clear-rate-limit path resets;
// here the WHERE is id-only (no tenant) because the caller is system-trusted
// or has already enforced ownership. Clearing already-clear columns is a safe
// idempotent no-op.
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
	_ = ctx
	now := s.now().UTC()

	// F-RATE-001 §1.6: evaluate per-account temp-unschedulable rules and
	// custom error codes when the feature is enabled on this account.
	// This is ADDITIVE — evaluated first, before the existing 429/529 path,
	// so operators can classify any upstream status code including 403.
	if s.rulesProvider != nil {
		rules, customCodes := s.rulesProvider.GetAccountErrorRules(accountID)
		if len(rules) > 0 || len(customCodes) > 0 {
			if dec := evalAccountErrorRules(statusCode, respBody, rules, customCodes,
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
		retryAfterSeconds = durationSeconds(s.defaultCooldown)
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

// ClearCascade honors the rate.Service contract (rate.go §ClearCascade):
// atomically clear all cooldown state for one account. It delegates to the
// injected CooldownStateStore. A nil store (the zero-config default) keeps it a
// safe no-op so an unwired Service does not error. actorID is carried for the
// contract signature and audit symmetry but the store clears by id only — the
// admin HTTP path owns the tenant-scoped audit row.
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
	status := sessionWindow5hStatus(headers)
	if status == "" {
		return nil
	}
	now := s.now().UTC()
	windowEnd, ok := sessionWindow5hReset(headers, now)
	if !ok {
		return nil
	}
	return s.sessionWindows.UpdateProviderAccountSessionWindow5h(ctx, SessionWindowUpdate{
		ProviderAccountID: accountID,
		WindowStart:       windowEnd.Add(-sessionWindow5hDuration).UTC(),
		WindowEnd:         windowEnd.UTC(),
		Status:            status,
	})
}

func sessionWindow5hStatus(headers http.Header) string {
	if headers == nil {
		return ""
	}
	for _, name := range sessionWindow5hStatusHeaders {
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

func sessionWindow5hReset(headers http.Header, now time.Time) (time.Time, bool) {
	if headers == nil {
		return time.Time{}, false
	}
	var raw string
	for _, name := range sessionWindow5hResetHeaders {
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
	if resetAt.Before(now.Add(-sessionWindow5hDuration)) || resetAt.After(now.Add(7*24*time.Hour)) {
		return time.Time{}, false
	}
	return resetAt, true
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
