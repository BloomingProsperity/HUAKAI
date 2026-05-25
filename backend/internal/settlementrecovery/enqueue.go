package settlementrecovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// Enqueuer 抽象 dlq.Service.Enqueue,handler 注入 mock 用。
type Enqueuer interface {
	Enqueue(ctx context.Context, e dlq.Event) (int64, error)
}

// ErrEnqueuerNil 是 settle 失败但 enqueue 没配置的兜底报错 — 必须 P0 alert,
// 因为这意味着代码部署不完整(money path 兜底链断了)。
var ErrEnqueuerNil = errors.New("settlementrecovery: enqueuer not configured (post-delivery settle failure cannot be persisted)")

// EnqueuePayload 把 Payload 转 dlq.Event 并通过 Enqueuer 落表。
//
// 设计要点:
//   - idempotency key 含 tenant_id + claim_id + request_id,同一 settle 失败
//     重复 enqueue 走 ON CONFLICT 不重复行(usage_record_dlq 已有 unique idx)。
//   - source_table='billing_ledger_claims',source_id=claim_id,操作员从 admin
//     UI 能反查到原 claim 行。
//   - Lane=HIGH(LaneForKind 已映射),worker pool HIGH lane 优先消费。
//   - failureReason 由调用方传(描述 settle 失败的具体错),进 DLQ 行 failure_reason
//     给 ops 排查。
func EnqueuePayload(ctx context.Context, q Enqueuer, p Payload, failureReason string) (int64, error) {
	if q == nil {
		return 0, ErrEnqueuerNil
	}
	if err := p.Validate(); err != nil {
		return 0, fmt.Errorf("settlementrecovery: validate before enqueue: %w", err)
	}
	body, err := p.Encode()
	if err != nil {
		return 0, fmt.Errorf("settlementrecovery: encode payload: %w", err)
	}
	idem := buildIdempotencyKey(p)
	event := dlq.Event{
		TenantID:       p.Settle.TenantID,
		ClaimID:        p.Settle.ClaimID,
		EventKind:      dlq.EventKindPostDeliverySettlement,
		Lane:           dlq.LaneForKind(dlq.EventKindPostDeliverySettlement),
		Payload:        body,
		FailureReason:  failureReason,
		IdempotencyKey: idem,
		SourceTable:    "billing_ledger_claims",
		SourceID:       p.Settle.ClaimID,
		NextRetryAt:    time.Time{}, // store 默认按 policy 计算 first retry
	}
	return q.Enqueue(ctx, event)
}

// buildIdempotencyKey 生成稳定的 post_delivery_settlement DLQ 行 idempotency 字符串。
// 同 tenant + claim + request 重复 enqueue 走 ON CONFLICT(tenant_id, event_kind,
// idempotency_key, replica_target)只一行。
func buildIdempotencyKey(p Payload) string {
	return fmt.Sprintf("post_delivery_settlement:%d:%d:%s", p.Settle.TenantID, p.Settle.ClaimID, p.RequestID)
}
