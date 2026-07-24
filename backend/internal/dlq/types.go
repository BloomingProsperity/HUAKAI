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
	EventKindAuditMismatchRefund EventKind = "audit_mismatch_refund"
	EventKindAuditLedgerEntry    EventKind = "audit_ledger_entry"
	EventKindAccountHealth       EventKind = "account_health"
	EventKindMetrics             EventKind = "metrics"
	// EventKindPostDeliverySettlement 用于"流式/非流式响应已交付给客户端
	// 但 Tx2 settlement 未确认提交"的 durable recovery intent。
	// worker 拿到后重调 public Settler.Settle 重放(走完整 idempotency 路径,
	// 不重写底层 SQL),并用 claim/usage/billing_event 三证 proof 防重复扣费。
	// 当前结算恢复合同见 docs/HUAKAI工程设计手册.md §10.4。
	EventKindPostDeliverySettlement EventKind = "post_delivery_settlement"
	// EventKindCostReceiptAppend 用于 Tx2 已提交但 user_cost_receipts append
	// hook 失败的 durable recovery intent。worker 重放只派生并写 receipt,
	// 不重调 billing settle,避免成功收费后因 receipt 存储短故障丢用户凭证。
	EventKindCostReceiptAppend EventKind = "cost_receipt_append"
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
	// ErrUnretryable 标记"结构性不可重试"的失败(payload 损坏/校验不过/事件类型不匹配等):
	// 再重试同一份输入永远不会成功。handler 用 errors.Join/%w 把它裹进返回错,
	// 重试决策(NextFailureForErr)一旦 errors.Is 命中,立即把 record 转 quarantined,
	// 不再消耗重试预算；交付后结算事件例外，必须持续重试并告警。
	ErrUnretryable = errors.New("dlq: unretryable failure")
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
	case EventKindBillingEventReplica, EventKindAuditEventReplica, EventKindAuditMismatchRefund, EventKindUsageRecord, EventKindAuditLedgerEntry, EventKindPostDeliverySettlement, EventKindCostReceiptAppend:
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
	case EventKindAuditLedgerEntry:
		return ReplicaStatusNone
	default:
		return ReplicaStatusNone
	}
}
