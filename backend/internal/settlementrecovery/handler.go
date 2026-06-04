package settlementrecovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// Handler 是 obs/dlq worker 注册的 post_delivery_settlement 重放 handler。
//
// 流程:
//   1. Decode payload(失败 → 不可重试错,worker 转 quarantine)
//   2. Validate(同上)
//   3. 调 public billing.Settler.Settle 重放 — 走完整 Tx2 idempotency 路径
//   4. 若返 nil,标 DLQ delivered
//   5. 若返 billing.ErrClaimNotReserving(claim 已 committed),走 CommittedProof
//      三证:齐全 → 标 delivered;缺一 → 继续视失败重试
//   6. 其他错 → 视失败,worker 按 policy 重试 / quarantine
//
// 关键不变性:不重写底层 SQL,只通过 public Settler.Settle 入口,保 Tx2 单入口。
type Handler struct {
	Settler billing.Settler
	Proof   CommittedProof
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
		return fmt.Errorf("settlementrecovery: handler called with wrong event_kind=%q (want post_delivery_settlement)", record.EventKind)
	}

	payload, err := Decode(record.Payload)
	if err != nil {
		// Decode 失败 = payload corrupted,继续重试无意义。worker 按 policy
		// 多次失败后转 quarantined,operator 介入。
		return fmt.Errorf("settlementrecovery: decode payload: %w", err)
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("settlementrecovery: validate payload: %w", err)
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
	// worker 多次失败后转 quarantined,operator 决定是手动 force settle 还是 abort。
	return fmt.Errorf("settlementrecovery: settler returned ErrClaimNotReserving but proof says not committed for tenant=%d claim=%d", payload.Settle.TenantID, payload.Settle.ClaimID)
}
