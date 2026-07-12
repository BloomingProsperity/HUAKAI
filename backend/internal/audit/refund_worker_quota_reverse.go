package audit

import (
	"context"
	"expvar"
	"log/slog"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

var refundQuotaReverseSkippedTotal = expvar.NewInt("refund_quota_reverse_skipped_total")

// QuotaReverseResult 报告退款后的配额冲减结果。
type QuotaReverseResult struct {
	Skipped bool
}

// QuotaReverser 把已结算 claim 的成本从配额 settled_value 负向冲减(退款后的次级补账)。
// 退款只退钱包、不退配额计数会让用户净花费已降却因旧计数提前撞成本上限被拒(过度强制),
// 本接口在退款落库后按退款的实际金额把 settled_value 同步降回。实现由 wiring 注入(quota.Service
// 适配器);未注入则退款不触发配额冲减,行为与改造前完全一致(零回归)。
type QuotaReverser interface {
	ReverseSettledCost(ctx context.Context, tenantID, claimID int64, amountMicroUSD int64) (QuotaReverseResult, error)
}

// WithRefundQuotaReverser 注入配额成本冲减器:退款落库后,把退款的实际金额从配额 settled_value
// 负向冲减(post-commit 次级补账,fail-open)。不注入则退款不触发配额冲减(零行为变化)。
func WithRefundQuotaReverser(reverser QuotaReverser) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) {
		if reverser != nil {
			w.quotaReverser = reverser
		}
	}}
}

// reverseQuotaAfterRefund 在 billing 退款落库后,把退款的实际金额从配额 settled_value 负向冲减,
// 修补"退款只退钱包、配额计数没退→用户提前撞成本上限"的过度强制。语义要点:
//   - post-commit 次级补账、fail-open:失败只记 WARN 日志,不影响已成功的退款(与 quotaenforce 的
//     Settle/CommitCacheHit post-commit 补账一致;quota 刻意不接管外部事务)。
//   - 冲减额=本轮退款的实际金额 RefundMicroUSD(已被 billing 按 claim 原计费额累计封顶),与钱包退的
//     同一 delta 对齐。
//   - 防双冲减:① DLQ 重投在 Apply 入口被 pending(completed)/ 已存退款回执短路,到不了这里;② 即便
//     到了,billing 对同 audit_request_id 的幂等重放返回的是【存储的正退款额 + Idempotent=true】(不是
//     0,RefundMicroUSD<=0 守卫对此无效),故这里显式判 refund.Idempotent 跳过——本轮非新退款、settled_value
//     已在原始调用时冲过,绝不二次冲减。RefundMicroUSD<=0 守卫只覆盖"本就无退款"(amount=0 / 累计封顶到
//     remaining=0)这一类,不承担重放去重职责。
func (w *MismatchRefundWorker) reverseQuotaAfterRefund(ctx context.Context, payload MismatchRefundPayload, refund *billing.RefundResult) {
	if w == nil || w.quotaReverser == nil || refund == nil || refund.RefundMicroUSD <= 0 || refund.Idempotent {
		return
	}
	result, err := w.quotaReverser.ReverseSettledCost(ctx, payload.TenantID, payload.ClaimID, refund.RefundMicroUSD)
	if err != nil {
		slog.WarnContext(ctx, "退款后配额 settled_value 冲减失败(fail-open,不影响已成功退款)",
			slog.Int64("tenant_id", payload.TenantID),
			slog.Int64("claim_id", payload.ClaimID),
			slog.Int64("refund_micro_usd", refund.RefundMicroUSD),
			slog.String("error", err.Error()))
		return
	}
	if result.Skipped {
		refundQuotaReverseSkippedTotal.Add(1)
		slog.WarnContext(ctx, "退款后配额 settled_value 冲减跳过(预留未结算或无可冲减值)",
			slog.Int64("tenant_id", payload.TenantID),
			slog.Int64("claim_id", payload.ClaimID),
			slog.Int64("refund_micro_usd", refund.RefundMicroUSD))
	}
}
