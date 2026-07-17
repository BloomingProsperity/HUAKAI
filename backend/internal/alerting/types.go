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

type MetricType string

const (
	MetricTypeCPUUsagePercent MetricType = "cpu_usage_percent"
)

type EventState string

const (
	EventStateFiring         EventState = "firing"
	EventStateResolved       EventState = "resolved"
	EventStateManualResolved EventState = "manual_resolved"
)

type AlertRule struct {
	ID               int64
	TenantID         int64
	Name             string
	Metric           string
	MetricType       MetricType
	Comparator       Comparator
	Threshold        float64
	Severity         Severity
	WindowSeconds    int32
	SustainedSeconds int32
	CooldownSeconds  int32
	NotifyEmail      bool
	Filters          map[string]string
	LastTriggeredAt  *time.Time
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AlertEvent struct {
	ID             int64
	TenantID       int64
	RuleID         int64
	State          EventState
	ObservedValue  float64
	ThresholdValue *float64
	MetricValue    *float64
	Dimensions     map[string]string
	FiredAt        time.Time
	ResolvedAt     *time.Time
	EmailSent      bool
}

type AlertSilence struct {
	ID        int64
	TenantID  int64
	RuleID    *int64
	Reason    string
	StartsAt  time.Time
	EndsAt    time.Time
	Platform  string
	GroupID   string
	Region    string
	CreatedAt time.Time
}

type FiringNotice struct {
	RuleID        int64
	RuleName      string
	Metric        string
	MetricType    MetricType
	Comparator    Comparator
	Threshold     float64
	Severity      Severity
	ObservedValue float64
	Dimensions    map[string]string
	FiredAt       time.Time
}

type FiringDeliverer interface {
	DeliverFiring(context.Context, int64, FiringNotice) error
}

// FiringEmailDeliverer 返回 delivered=false 表示安全跳过（例如管理员收件人未配置）；
// error 只用于观测，Service 不把它传播到告警持久化主链。
type FiringEmailDeliverer interface {
	DeliverFiringEmail(context.Context, int64, FiringNotice) (delivered bool, err error)
}

type FiringDeliveryErrorRecorder func(context.Context, int64, FiringNotice, error)

type CreateRuleInput struct {
	TenantID         int64
	Name             string
	Metric           string
	MetricType       MetricType
	Comparator       Comparator
	Threshold        float64
	Severity         Severity
	WindowSeconds    int32
	SustainedSeconds int32
	CooldownSeconds  int32
	NotifyEmail      bool
	Filters          map[string]string
	Enabled          *bool
}

type UpdateRuleInput struct {
	TenantID         int64
	ID               int64
	Name             *string
	Metric           *string
	MetricType       *MetricType
	Comparator       *Comparator
	Threshold        *float64
	Severity         *Severity
	WindowSeconds    *int32
	SustainedSeconds *int32
	CooldownSeconds  *int32
	NotifyEmail      *bool
	Filters          *map[string]string
	Enabled          *bool
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
	Platform string
	GroupID  string
	Region   string
}

type UpsertFiringEventInput struct {
	TenantID       int64
	RuleID         int64
	ObservedValue  float64
	ThresholdValue float64
	MetricValue    float64
	Dimensions     map[string]string
	FiredAt        time.Time
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

	UpsertFiringEvent(context.Context, UpsertFiringEventInput) (AlertEvent, bool, error)
	ResolveFiringEvent(context.Context, int64, int64, time.Time) (AlertEvent, bool, error)
	ManualResolveEvent(context.Context, int64, int64, time.Time) (AlertEvent, error)
	MarkEventEmailSent(context.Context, int64, int64) (AlertEvent, error)
	MarkRuleTriggered(context.Context, int64, int64, time.Time) error
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
