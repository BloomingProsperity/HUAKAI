package audit

import (
	"context"
	"expvar"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

var refundQuotaReverseSkippedTotal = expvar.NewInt("refund_quota_reverse_skipped_total")

// QuotaReverseResult 报告退款后的配额冲减结果。
type QuotaReverseResult struct {
	Skipped bool
}

// QuotaReverser 为不具备组合事务的兼容路径提供退款后配额冲减。
type QuotaReverser interface {
	ReverseSettledCost(ctx context.Context, tenantID, claimID int64, amountMicroUSD int64) (QuotaReverseResult, error)
}

// QuotaReverserInTx 把配额冲减加入退款所属事务，保证钱包与强配额一起提交或回滚。
type QuotaReverserInTx interface {
	ReverseSettledCostInTx(ctx context.Context, tx pgx.Tx, tenantID, claimID int64, amountMicroUSD int64) (QuotaReverseResult, error)
}

// WithRefundQuotaReverser 注入配额成本冲减器。事务退款要求实现 QuotaReverserInTx；
// 兼容路径仍使用基础接口执行提交后冲减。
func WithRefundQuotaReverser(reverser QuotaReverser) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) {
		if reverser != nil {
			w.quotaReverser = reverser
		}
	}}
}

// reverseQuotaAfterLegacyRefund 仅服务无法共享 PostgreSQL 事务的兼容部署。
// 正式 PostgreSQL 路径在 applyInTxOnce 内完成原子冲减，不经过这里。
func (w *MismatchRefundWorker) reverseQuotaAfterLegacyRefund(ctx context.Context, payload MismatchRefundPayload, refund *billing.RefundResult) {
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
