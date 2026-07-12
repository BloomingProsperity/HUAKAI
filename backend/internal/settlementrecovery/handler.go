package settlementrecovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

// Handler 是 obs/dlq worker 注册的 post_delivery_settlement 重放 handler。
//
// 流程:
//  1. Decode payload(失败 → 结构错误并持续封顶退避，等待运维修复)
//  2. Validate(同上)
//  3. 调 public billing.Settler.Settle 重放 — 走完整 Tx2 idempotency 路径
//  4. 若返 nil,标 DLQ delivered
//  5. 若返 billing.ErrClaimNotReserving(claim 已 committed),走 CommittedProof
//     三证:齐全 → 标 delivered;缺一 → 继续视失败重试
//  6. 其他错 → 视失败,worker 持续封顶退避并在越过阈值后告警
//
// 关键不变性:不重写底层 SQL,只通过 public Settler.Settle 入口,保 Tx2 单入口。
type Handler struct {
	Settler        billing.Settler
	Proof          CommittedProof
	AuditRefPolicy *eventbus.AuditRefPolicy
}

// ErrSettlerNil/ErrProofNil 是 handler wire 不完整的兜底,
// 配合排查("启动时部署 wire 缺")。
var (
	ErrSettlerNil = errors.New("settlementrecovery: handler Settler not configured")
	ErrProofNil   = errors.New("settlementrecovery: handler CommittedProof not configured")
)

// Handle 由 dlq worker 调用。
//   - 返 nil:worker 标 record delivered。
//   - 返 err:worker 按 policy 重试 / quarantine,并把 err 写 replay_failure_reason。
func (h *Handler) Handle(ctx context.Context, record dlq.Record) error {
	if h.Settler == nil {
		return ErrSettlerNil
	}
	if h.Proof == nil {
		return ErrProofNil
	}
	if record.EventKind != dlq.EventKindPostDeliverySettlement {
		// 事件类型不匹配 = 路由/入队错配,重试同一行永远是错的类型 → 结构性不可重试。
		return fmt.Errorf("settlementrecovery: handler called with wrong event_kind=%q (want post_delivery_settlement): %w", record.EventKind, dlq.ErrUnretryable)
	}

	payload, err := Decode(record.Payload)
	if err != nil {
		// Decode 失败属于结构性损坏；错误分类触发告警，但交付后钱账仍保持持续重试。
		return fmt.Errorf("settlementrecovery: decode payload: %w", errors.Join(err, dlq.ErrUnretryable))
	}
	if err := payload.Validate(); err != nil {
		// 校验不过同属结构性损坏，保留分类供运维定位。
		return fmt.Errorf("settlementrecovery: validate payload: %w", errors.Join(err, dlq.ErrUnretryable))
	}
	if err := payload.ValidateAuditRef(h.AuditRefPolicy); err != nil {
		// 审计证据可能由运维修复或 audit-DLQ 后续补齐；保持 pending 重试，
		// 绝不能先扣费，也不能把该缺口伪装成 delivered。
		return err
	}

	req := payload.ToSettleRequest()
	_, settleErr := h.Settler.Settle(ctx, req)
	if settleErr == nil {
		return nil
	}
	if !errors.Is(settleErr, billing.ErrClaimNotReserving) {
		// 其他错(DB error / lock conflict / 上下游异常)→ 继续重试。
		return fmt.Errorf("settlementrecovery: settler.Settle: %w", settleErr)
	}

	// ErrClaimNotReserving:claim 不在 reserving 状态。要么已 committed
	// (说明上次重 settle 已成功),要么 aborted / 其他 — 走三证 proof 验。
	committed, proofErr := h.Proof.IsCommitted(ctx, payload.Settle.TenantID, payload.Settle.ClaimID)
	if proofErr != nil {
		return fmt.Errorf("settlementrecovery: committed proof: %w", proofErr)
	}
	if committed {
		// 三证齐 = settle 已成功,本 DLQ 重放视 idempotent success。
		return nil
	}
	// 三证未齐 = claim 状态异常(可能 aborted 或半提交),继续报错重试。
	// worker 持续重试并告警，由运维修复异常状态。
	return fmt.Errorf("settlementrecovery: settler returned ErrClaimNotReserving but proof says not committed for tenant=%d claim=%d", payload.Settle.TenantID, payload.Settle.ClaimID)
}
