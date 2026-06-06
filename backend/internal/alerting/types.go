package alerting

import (
	"context"
	"errors"
	"time"
)

type Comparator string

const (
	ComparatorGT  Comparator = "gt"
	ComparatorGTE Comparator = "gte"
	ComparatorLT  Comparator = "lt"
	ComparatorLTE Comparator = "lte"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type EventState string

const (
	EventStateFiring   EventState = "firing"
	EventStateResolved EventState = "resolved"
)

type AlertRule struct {
	ID            int64
	TenantID      int64
	Name          string
	Metric        string
	Comparator    Comparator
	Threshold     float64
	Severity      Severity
	WindowSeconds int32
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AlertEvent struct {
	ID            int64
	TenantID      int64
	RuleID        int64
	State         EventState
	ObservedValue float64
	FiredAt       time.Time
	ResolvedAt    *time.Time
}

type AlertSilence struct {
	ID        int64
	TenantID  int64
	RuleID    *int64
	Reason    string
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedAt time.Time
}

type CreateRuleInput struct {
	TenantID      int64
	Name          string
	Metric        string
	Comparator    Comparator
	Threshold     float64
	Severity      Severity
	WindowSeconds int32
	Enabled       *bool
}

type UpdateRuleInput struct {
	TenantID      int64
	ID            int64
	Name          *string
	Metric        *string
	Comparator    *Comparator
	Threshold     *float64
	Severity      *Severity
	WindowSeconds *int32
	Enabled       *bool
}

type ListRulesInput struct {
	TenantID int64
	Limit    int
	Offset   int
}

type CreateSilenceInput struct {
	TenantID int64
	RuleID   *int64
	Reason   string
	StartsAt time.Time
	EndsAt   time.Time
}

type ListSilencesInput struct {
	TenantID int64
	Limit    int
	Offset   int
}

type ListEventsInput struct {
	TenantID int64
	RuleID   *int64
	State    EventState
	Limit    int
	Offset   int
}

type Store interface {
	CreateRule(context.Context, AlertRule) (AlertRule, error)
	UpdateRule(context.Context, AlertRule) (AlertRule, error)
	DeleteRule(context.Context, int64, int64) error
	GetRule(context.Context, int64, int64) (AlertRule, error)
	ListRules(context.Context, ListRulesInput) ([]AlertRule, error)
	ListEnabledRules(context.Context, int64) ([]AlertRule, error)
	ListTenantsWithEnabledRules(context.Context) ([]int64, error)

	UpsertFiringEvent(context.Context, int64, int64, float64, time.Time) (AlertEvent, error)
	ResolveFiringEvent(context.Context, int64, int64, time.Time) (AlertEvent, bool, error)
	ListEvents(context.Context, ListEventsInput) ([]AlertEvent, error)

	CreateSilence(context.Context, AlertSilence) (AlertSilence, error)
	DeleteSilence(context.Context, int64, int64) error
	ListSilences(context.Context, ListSilencesInput) ([]AlertSilence, error)
	ListActiveSilences(context.Context, int64, time.Time) ([]AlertSilence, error)
}

var (
	ErrInvalidInput       = errors.New("alerting: invalid input")
	ErrNotFound           = errors.New("alerting: not found")
	ErrRuleExists         = errors.New("alerting: rule exists")
	ErrStoreNotConfigured = errors.New("alerting: store not configured")
)
