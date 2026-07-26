package settlementrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// Enqueuer 抽象 dlq.Service.Enqueue,handler 注入 mock 用。
type Enqueuer interface {
	Enqueue(ctx context.Context, e dlq.Event) (int64, error)
}

// ErrEnqueuerNil 是 settle 失败但 enqueue 没配置的兜底报错 — 必须 P0 alert,
// 因为这意味着代码部署不完整(money path 兜底链断了)。
var ErrEnqueuerNil = errors.New("settlementrecovery: enqueuer not configured (post-delivery settle failure cannot be persisted)")

const failureEnqueueTimeout = 10 * time.Second

// FailureEvidence 是恢复队列与结算意图表共用的最小持久证据。
// Payload 只包含已经过 Payload 合同校验的结算重放数据，FailureClass
// 是脱敏稳定分类；调用方可在队列写入失败时把它存入结算意图。
type FailureEvidence struct {
	Payload      json.RawMessage
	FailureClass string
}

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
	return enqueueEncodedPayload(ctx, q, p, body, failureReason)
}

func enqueueEncodedPayload(
	ctx context.Context,
	q Enqueuer,
	p Payload,
	body json.RawMessage,
	failureReason string,
) (int64, error) {
	if q == nil {
		return 0, ErrEnqueuerNil
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

// EnqueueFailure 用独立短上下文持久化结算失败；队列自身失败发脱敏 P0 critical
// 信号，并把同一份可持久证据返回给结算意图旁路。
func EnqueueFailure(
	ctx context.Context,
	q Enqueuer,
	p Payload,
	settleErr error,
	component string,
) (FailureEvidence, error) {
	failureClass := privacy.ErrorClassFor(ctx, settleErr)
	evidence := FailureEvidence{FailureClass: failureClass}
	if err := p.Validate(); err != nil {
		enqueueErr := fmt.Errorf("settlementrecovery: validate before enqueue: %w", err)
		logEnqueueFailure(ctx, p, component, failureClass, enqueueErr)
		return evidence, enqueueErr
	}
	body, err := p.Encode()
	if err != nil {
		enqueueErr := fmt.Errorf("settlementrecovery: encode payload: %w", err)
		logEnqueueFailure(ctx, p, component, failureClass, enqueueErr)
		return evidence, enqueueErr
	}
	evidence.Payload = append(json.RawMessage(nil), body...)
	enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureEnqueueTimeout)
	defer cancel()
	_, enqueueErr := enqueueEncodedPayload(enqueueCtx, q, p, body, failureClass)
	if enqueueErr == nil {
		return evidence, nil
	}
	logEnqueueFailure(enqueueCtx, p, component, failureClass, enqueueErr)
	return evidence, enqueueErr
}

func logEnqueueFailure(
	ctx context.Context,
	p Payload,
	component string,
	failureClass string,
	enqueueErr error,
) {
	if component == "" {
		component = "settlementrecovery.enqueue"
	}
	req := p.ToSettleRequest()
	_ = privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity: privacy.SeverityCritical, Component: component,
		RequestID: p.RequestID, ErrorClass: privacy.ErrorClassFor(ctx, enqueueErr),
		Attrs: map[string]any{
			"event_class": "money_lost_double_fault", "event_type": string(p.Source),
			"priority": "P0", "tenant_id": req.TenantID, "claim_id": req.ClaimID,
			"failure_reason_class": failureClass, "recovery_failure_class": privacy.ErrorClassFor(ctx, enqueueErr),
		},
	})
}

// buildIdempotencyKey 生成稳定的 post_delivery_settlement DLQ 行 idempotency 字符串。
// 同 tenant + claim + request 重复 enqueue 走 ON CONFLICT(tenant_id, event_kind,
// idempotency_key, replica_target)只一行。
func buildIdempotencyKey(p Payload) string {
	return fmt.Sprintf("post_delivery_settlement:%d:%d:%s", p.Settle.TenantID, p.Settle.ClaimID, p.RequestID)
}
