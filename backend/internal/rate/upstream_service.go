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

type sessionWindowDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type PostgresSessionWindowStore struct {
	db sessionWindowDB
}

type upstreamRateService struct {
	now             func() time.Time
	defaultCooldown time.Duration
	sessionWindows  SessionWindowStore
}

func NewUpstreamRateService(clock func() time.Time, defaultCooldown time.Duration) Service {
	return NewUpstreamRateServiceWithSessionWindowStore(clock, defaultCooldown, nil)
}

func NewUpstreamRateServiceWithSessionWindowStore(clock func() time.Time, defaultCooldown time.Duration, store SessionWindowStore) Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if defaultCooldown <= 0 {
		defaultCooldown = defaultUpstreamCooldown
	}
	return &upstreamRateService{now: clock, defaultCooldown: defaultCooldown, sessionWindows: store}
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

func (s *upstreamRateService) HandleUpstreamError(ctx context.Context, accountID int64, statusCode int, respHeaders http.Header, respBody []byte) (Decision, error) {
	_ = ctx
	_ = accountID
	_ = respBody
	if statusCode != http.StatusTooManyRequests && statusCode != 529 {
		return Decision{}, nil
	}
	now := s.now().UTC()
	until, retryAfterSeconds, ok := retryAfterCooldown(respHeaders, now)
	if !ok {
		retryAfterSeconds = durationSeconds(s.defaultCooldown)
		until = now.Add(time.Duration(retryAfterSeconds) * time.Second)
	}
	dec := Decision{
		CooldownUntil:     until.UTC(),
		ShouldFailover:    true,
		RetryAfterSeconds: retryAfterSeconds,
	}
	if statusCode == 529 {
		dec.StateChange = StateOverloaded
		dec.Reason = ReasonOverloaded
		return dec, nil
	}
	dec.StateChange = StateRateLimited
	dec.Reason = ReasonRateLimitRPM
	return dec, nil
}

func (s *upstreamRateService) ClearCascade(ctx context.Context, accountID int64, actorID string) error {
	_ = ctx
	_ = accountID
	_ = actorID
	return nil
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
