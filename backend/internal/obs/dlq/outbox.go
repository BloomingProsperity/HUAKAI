package dlq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Outbox interface {
	Enqueue(context.Context, OutboxEvent) (OutboxEvent, error)
	Dequeue(context.Context, DequeueOptions) (OutboxEvent, bool, error)
	MarkCompleted(context.Context, string) error
	MarkFailedRetry(context.Context, string, string, time.Time) error
	MarkFailedDead(context.Context, string, string) error
}

type Handler func(context.Context, OutboxEvent) error

type RetryPolicy struct {
	MaxAttempts int
	MaxBackoff  time.Duration
	Schedule    []time.Duration
}

type RuntimeConfig struct {
	MaxAttempts  int
	MaxBackoff   time.Duration
	DrainTimeout time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 5,
		MaxBackoff:  15 * time.Minute,
		Schedule: []time.Duration{
			time.Second,
			5 * time.Second,
			30 * time.Second,
			5 * time.Minute,
			30 * time.Minute,
		},
	}
}

func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		MaxAttempts:  5,
		MaxBackoff:   15 * time.Minute,
		DrainTimeout: 30 * time.Second,
	}
}

func LoadRuntimeConfigFromEnv() (RuntimeConfig, error) {
	cfg := DefaultRuntimeConfig()
	var err error
	if cfg.MaxAttempts, err = envPositiveInt("HUAKAI_DLQ_MAX_ATTEMPTS", cfg.MaxAttempts); err != nil {
		return RuntimeConfig{}, err
	}
	mins, err := envPositiveInt("HUAKAI_DLQ_MAX_BACKOFF_MIN", int(cfg.MaxBackoff/time.Minute))
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.MaxBackoff = time.Duration(mins) * time.Minute
	seconds, err := envPositiveInt("HUAKAI_DLQ_DRAIN_TIMEOUT_SECONDS", int(cfg.DrainTimeout/time.Second))
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.DrainTimeout = time.Duration(seconds) * time.Second
	return cfg, nil
}

func (p RetryPolicy) normalized() RetryPolicy {
	def := DefaultRetryPolicy()
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = def.MaxAttempts
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = def.MaxBackoff
	}
	if len(p.Schedule) == 0 {
		p.Schedule = append([]time.Duration(nil), def.Schedule...)
	}
	return p
}

func (p RetryPolicy) NextDelay(previousAttempts int) (time.Duration, bool) {
	p = p.normalized()
	nextAttempt := previousAttempts + 1
	if nextAttempt >= p.MaxAttempts {
		return 0, true
	}
	idx := previousAttempts
	if idx >= len(p.Schedule) {
		idx = len(p.Schedule) - 1
	}
	delay := p.Schedule[idx]
	if delay > p.MaxBackoff {
		delay = p.MaxBackoff
	}
	return delay, false
}

func normalizeEvent(e OutboxEvent, now time.Time) (OutboxEvent, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(e.ID) == "" {
		e.ID = newEventID()
	}
	e.ID = strings.TrimSpace(e.ID)
	e.EventType = strings.TrimSpace(e.EventType)
	if e.EventType == "" {
		e.EventType = "generic"
	}
	if e.TenantID <= 0 || e.ID == "" {
		return OutboxEvent{}, ErrInvalidEvent
	}
	e.Priority = normalizePriority(e.Priority)
	e.Status = normalizeStatus(e.Status)
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.NextRetryAt.IsZero() {
		e.NextRetryAt = now
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(e.Payload) {
		return OutboxEvent{}, fmt.Errorf("%w: payload json", ErrInvalidEvent)
	}
	e.Payload = SanitizePayload(e.Payload)
	e.FailureReason = RedactString(e.FailureReason)
	return e, nil
}

func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func envPositiveInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("obsdlq: invalid %s=%q", name, raw)
	}
	return n, nil
}
