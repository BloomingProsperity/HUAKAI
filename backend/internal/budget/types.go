package budget

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"math"
	"strconv"
	"time"
)

const (
	CodeLimitExceeded     = "budget_limit_exceeded"
	CodeBudgetUnavailable = "budget_unavailable"
)

var (
	ErrDenied      = errors.New("budget: denied")
	ErrUnavailable = errors.New("budget: unavailable")

	budgetFailOpenTotal = expvar.NewInt("budget_fail_open_total")
)

type FailMode string

const (
	FailModeOpen           FailMode = "open"
	FailModeClosed         FailMode = "closed"
	FailModeMemoryFallback FailMode = "memory_fallback"
)

type ScopeKind string

const (
	ScopeUser      ScopeKind = "user"
	ScopeAPIKey    ScopeKind = "api_key"
	ScopePoolGroup ScopeKind = "pool_group"
)

type Counter string

const (
	CounterRPM Counter = "rpm"
	CounterTPM Counter = "tpm"
)

type Scope struct {
	TenantID int64
	Kind     ScopeKind
	ID       string
	Model    string
}

type LimitPair struct {
	RPM int64 `json:"rpm"`
	TPM int64 `json:"tpm"`
}

func (p LimitPair) normalized() LimitPair {
	return LimitPair{RPM: normalizeLimit(p.RPM), TPM: normalizeLimit(p.TPM)}
}

func normalizeLimit(v int64) int64 {
	if v <= 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return v
}

type LimitSpec struct {
	LimitPair
	Models map[string]LimitPair `json:"models,omitempty"`
}

type StaticLimitsProvider struct {
	Default    LimitPair
	Users      map[int64]LimitSpec
	Keys       map[int64]LimitSpec
	PoolGroups map[int64]LimitSpec
}

type LimitsProvider interface {
	Scopes(context.Context, ReserveRequest) ([]ScopeLimit, error)
}

type ScopeLimit struct {
	Scope  Scope
	Limits LimitPair
}

type ReserveRequest struct {
	TenantID       int64
	ClaimID        int64
	UserID         int64
	APIKeyID       int64
	PoolGroupID    int64
	RequestedModel string
	ReservedTokens int64
	At             time.Time
}

type ReserveResult struct {
	Allowed        bool
	IdempotencyHit bool
	FailOpen       bool
	Decision       Decision
}

type Decision struct {
	Code       string
	Counter    Counter
	Scope      Scope
	Current    int64
	Limit      int64
	RetryAfter time.Duration
	Reason     string
}

type SettleRequest struct {
	TenantID     int64
	ClaimID      int64
	ActualTokens int64
}

type ReleaseRequest struct {
	TenantID int64
	ClaimID  int64
	Reason   string
}

type DenyError struct {
	Decision Decision
	Cause    error
}

func (e *DenyError) Error() string {
	if e == nil {
		return "budget: denied"
	}
	if e.Decision.Code != "" {
		return fmt.Sprintf("budget: %s", e.Decision.Code)
	}
	return "budget: denied"
}

func (e *DenyError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return ErrDenied
	}
	return e.Cause
}

func IsDenied(err error) bool {
	if err == nil {
		return false
	}
	var deny *DenyError
	return errors.As(err, &deny) || errors.Is(err, ErrDenied)
}

func intString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func minuteRetryAfter(minute int64, now time.Time) time.Duration {
	next := time.Unix((minute+1)*60, 0).UTC()
	d := next.Sub(now.UTC())
	if d <= 0 {
		return time.Second
	}
	if d > time.Minute {
		return time.Minute
	}
	return d
}
