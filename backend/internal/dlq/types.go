package dlq

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type EventKind string

const (
	EventKindUsageRecord         EventKind = "usage_record"
	EventKindBillingEventReplica EventKind = "billing_event_replica"
	EventKindAuditEventReplica   EventKind = "audit_event_replica"
	EventKindAccountHealth       EventKind = "account_health"
	EventKindMetrics             EventKind = "metrics"
)

type Lane string

const (
	LaneHigh Lane = "HIGH"
	LaneMed  Lane = "MED"
	LaneLow  Lane = "LOW"
)

type Status string

const (
	StatusPending        Status = "pending"
	StatusInflight       Status = "inflight"
	StatusDelivered      Status = "delivered"
	StatusOperatorReview Status = "operator_review"
	StatusDLQ            Status = "dlq"
	StatusQuarantined    Status = "quarantined"
)

const (
	ReplicaStatusNone      = "none"
	ReplicaStatusPending   = "pending"
	ReplicaStatusDelivered = "delivered"
	ReplicaStatusFailed    = "failed"
)

var (
	ErrStoreNotConfigured = errors.New("dlq: store not configured")
	ErrInvalidEvent       = errors.New("dlq: invalid event")
	ErrNoHandler          = errors.New("dlq: handler not registered")
	ErrNotFound           = errors.New("dlq: record not found")
)

type Event struct {
	TenantID       int64
	ClaimID        int64
	EventKind      EventKind
	Lane           Lane
	Payload        json.RawMessage
	FailureReason  string
	ReplicaTarget  string
	ReplicaStatus  string
	IdempotencyKey string
	SourceTable    string
	SourceID       int64
	NextRetryAt    time.Time
	LeaseTTL       time.Duration
}

type Record struct {
	ID                  int64              `json:"id"`
	TenantID            int64              `json:"tenant_id"`
	ClaimID             *int64             `json:"claim_id,omitempty"`
	Payload             json.RawMessage    `json:"payload"`
	FailureReason       string             `json:"failure_reason"`
	FailureAt           time.Time          `json:"failure_at"`
	ReplayAttempts      int                `json:"replay_attempts"`
	LastReplayAt        pgtype.Timestamptz `json:"-"`
	ReplayedAt          pgtype.Timestamptz `json:"-"`
	ReplayFailureReason *string            `json:"replay_failure_reason,omitempty"`
	EventKind           EventKind          `json:"event_kind"`
	Lane                Lane               `json:"lane"`
	Status              Status             `json:"status"`
	NextRetryAt         time.Time          `json:"next_retry_at"`
	LeaseOwner          *string            `json:"lease_owner,omitempty"`
	LeaseUntil          pgtype.Timestamptz `json:"-"`
	ReplicaStatus       string             `json:"replica_status"`
	ReplicaTarget       string             `json:"replica_target"`
	ReplicaCommittedAt  pgtype.Timestamptz `json:"-"`
	IdempotencyKey      string             `json:"idempotency_key"`
	SourceTable         string             `json:"source_table"`
	SourceID            *int64             `json:"source_id,omitempty"`
	OperatorReviewAt    pgtype.Timestamptz `json:"-"`
}

func LaneForKind(kind EventKind) Lane {
	switch kind {
	case EventKindBillingEventReplica, EventKindAuditEventReplica, EventKindUsageRecord:
		return LaneHigh
	case EventKindAccountHealth:
		return LaneMed
	case EventKindMetrics:
		return LaneLow
	default:
		return LaneMed
	}
}

func ReplicaStatusForKind(kind EventKind) string {
	switch kind {
	case EventKindBillingEventReplica, EventKindAuditEventReplica:
		return ReplicaStatusPending
	default:
		return ReplicaStatusNone
	}
}
