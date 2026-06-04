// HUAKAI · iKun

package payment

import "time"

// payment_audit_events 事件类型 (决策 A 独立领域审计表)。
// 与 billing_events 职责分离: billing_events 记成功入账的钱事实, 本表记操作轨迹。
const (
	AuditOrderCreated       = "order_created"
	AuditPaidConfirmed      = "paid_confirmed"
	AuditFulfillmentStarted = "fulfillment_started"
	AuditCredited           = "credited"
	AuditFulfillmentFailed  = "fulfillment_failed"
	AuditIdempotentReplay   = "idempotent_replay"
	AuditOrderExpired       = "order_expired"
	AuditOrderCancelled     = "order_cancelled"
)

const (
	ActorKindAdmin  = "admin"
	ActorKindUser   = "user"
	ActorKindSystem = "system"
)

// AuditEvent 钱路径操作审计记录 (payment_audit_events 一行)。
type AuditEvent struct {
	ID          int64
	TenantID    int64
	OrderID     int64
	EventType   string
	ActorKind   string
	ActorID     int64
	ReasonClass string
	RequestID   string
	Payload     map[string]any
	OccurredAt  time.Time
}
