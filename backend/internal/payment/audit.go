// HUAKAI · iKun

package payment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

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
	AuditOrderRefunded      = "order_refunded"
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

// sanitizeAuditPayload 在写入支付审计前清除敏感数据，禁止记录商户密钥、完整回调体
// 或个人支付材料。无法安全投影时保留明确的脱敏结果，而非回写原始载荷。
func sanitizeAuditPayload(ctx context.Context, payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	raw := privacy.SafePayloadOrBlocked(ctx, payload)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{
			"redaction_result": privacy.RedactionResultBlocked,
			"error_class":      privacy.ErrorClassPrivacyGuardHit,
		}
	}
	return out
}
