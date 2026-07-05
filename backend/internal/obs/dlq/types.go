package dlq

import (
	"encoding/json"
	"errors"
	"time"
)

type Priority string

const (
	PriorityAny      Priority = ""
	PriorityDefault  Priority = "default"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

type Status string

const (
	StatusPending     Status = "pending"
	StatusProcessing  Status = "processing"
	StatusCompleted   Status = "completed"
	StatusFailedRetry Status = "failed_retry"
	StatusFailedDead  Status = "failed_dead"
)

const (
	EventTypeEmailRetry   = "email.retry"
	EventTypeChannelAlert = "channel.alert"
	EventTypeAdminAlert   = "admin.alert"
)

var (
	ErrOutboxNotConfigured = errors.New("obsdlq: outbox not configured")
	ErrInvalidEvent        = errors.New("obsdlq: invalid event")
	ErrEventNotFound       = errors.New("obsdlq: event not found")
	ErrNoHandler           = errors.New("obsdlq: no handler registered")
)

type OutboxEvent struct {
	ID            string          `json:"id"`
	TenantID      int64           `json:"tenant_id"`
	EventType     string          `json:"event_type"`
	Priority      Priority        `json:"priority"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
	AttemptCount  int             `json:"attempt_count"`
	NextRetryAt   time.Time       `json:"next_retry_at"`
	Status        Status          `json:"status"`
	FailureReason string          `json:"failure_reason,omitempty"`
}

type DeadEvent struct {
	ID            string          `json:"id"`
	OutboxEventID string          `json:"outbox_event_id"`
	TenantID      int64           `json:"tenant_id"`
	Payload       json.RawMessage `json:"payload"`
	DeadAt        time.Time       `json:"dead_at"`
	DeadReason    string          `json:"dead_reason"`
}

type DequeueOptions struct {
	Priority          Priority
	Now               time.Time
	WorkerID          string
	VisibilityTimeout time.Duration
}

func (e OutboxEvent) clone() OutboxEvent {
	if e.Payload != nil {
		e.Payload = append(json.RawMessage(nil), e.Payload...)
	}
	return e
}

func priorityRank(p Priority) int {
	switch p {
	case PriorityCritical:
		return 3
	case PriorityHigh:
		return 2
	case PriorityDefault, PriorityAny:
		return 1
	default:
		return 0
	}
}

func normalizePriority(p Priority) Priority {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityDefault:
		return p
	default:
		return PriorityDefault
	}
}

func normalizeStatus(s Status) Status {
	switch s {
	case StatusPending, StatusProcessing, StatusCompleted, StatusFailedRetry, StatusFailedDead:
		return s
	default:
		return StatusPending
	}
}
