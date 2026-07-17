package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbaudit "github.com/BloomingProsperity/HUAKAI/internal/db/audit"
)

const maxDisputeCostMicroUSD int64 = 1<<63 - 1

var maxDisputeCostMicroUSDDecimal = decimal.NewFromInt(maxDisputeCostMicroUSD)

// DisputeRefundExecutor 把既有退款执行器约束为可加入调用方事务的最小接口。
type DisputeRefundExecutor interface {
	RefundInTx(context.Context, pgx.Tx, billing.RefundRequest) (*billing.RefundResult, error)
}

// CostDisputeResolver 把终态裁决与退款账务效果收在同一个数据库事务内。
type CostDisputeResolver struct {
	pool     *pgxpool.Pool
	refunder DisputeRefundExecutor
}

func NewCostDisputeResolver(pool *pgxpool.Pool, refunder DisputeRefundExecutor) (*CostDisputeResolver, error) {
	if pool == nil || refunder == nil {
		return nil, ErrDisputeResolverRequired
	}
	return &CostDisputeResolver{pool: pool, refunder: refunder}, nil
}

func (s *CostDisputeResolver) ResolveDispute(ctx context.Context, in ResolveCostDisputeInput) (ResolveCostDisputeResult, error) {
	if s == nil || s.pool == nil || s.refunder == nil {
		return ResolveCostDisputeResult{}, ErrDisputeResolverRequired
	}
	if err := validateResolveDispute(in); err != nil {
		return ResolveCostDisputeResult{}, err
	}

	// 默认 READ COMMITTED 让并发 UPDATE 在等待行锁后重新检查状态守卫；第二个
	// 裁决稳定得到零行，而不是把可重试的序列化错误暴露给运营端。
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ResolveCostDisputeResult{}, fmt.Errorf("audit: begin dispute resolution tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := dbaudit.New(tx).ResolveCostDispute(ctx, dbaudit.ResolveCostDisputeParams{
		TenantID:     in.TenantID,
		ID:           in.ID,
		Status:       strings.TrimSpace(in.Status),
		OperatorNote: strings.TrimSpace(in.OperatorNote),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResolveCostDisputeResult{}, ErrDisputeNotResolvable
		}
		return ResolveCostDisputeResult{}, fmt.Errorf("audit: update dispute state: %w", err)
	}

	dispute := costDisputeFromDB(row)
	result := ResolveCostDisputeResult{Dispute: dispute}
	if dispute.Status == DisputeStatusResolved {
		refund, refundErr := s.refundResolvedDisputeInTx(ctx, tx, dispute)
		if refundErr != nil {
			return ResolveCostDisputeResult{}, refundErr
		}
		result.RefundMicroUSD = refund.RefundMicroUSD
		result.RefundAdjustmentRef = refund.AdjustmentRef
		result.RefundIdempotent = refund.Idempotent
	}

	if err := tx.Commit(ctx); err != nil {
		return ResolveCostDisputeResult{}, fmt.Errorf("audit: commit dispute resolution tx: %w", err)
	}
	return result, nil
}

func (s *CostDisputeResolver) refundResolvedDisputeInTx(ctx context.Context, tx pgx.Tx, dispute CostDispute) (*billing.RefundResult, error) {
	if s == nil || s.refunder == nil || tx == nil || dispute.TenantID <= 0 || dispute.DisputeID == "" || dispute.RequestID == "" || dispute.Status != DisputeStatusResolved {
		return nil, ErrDisputeInvalid
	}

	var (
		claimID    int64
		actualCost decimal.Decimal
	)
	// 正常幂等链应只产生一条 committed claim；若历史异常留下多条，选择最新一条，
	// 同时仍由退款执行器的 claim 行锁、审计请求幂等键和累计上限阻止多退。
	err := tx.QueryRow(ctx, `
SELECT id, COALESCE(actual_cost, 0)
FROM billing_ledger_claims
WHERE tenant_id = $1
  AND logical_request_id = $2
  AND status = 'committed'
ORDER BY id DESC
LIMIT 1`, dispute.TenantID, dispute.RequestID).Scan(&claimID, &actualCost)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDisputeNoCharge
		}
		return nil, fmt.Errorf("audit: find committed dispute claim: %w", err)
	}

	amountMicroUSD, err := disputeCostUSDToMicros(actualCost)
	if err != nil {
		return nil, err
	}
	if amountMicroUSD <= 0 {
		return nil, ErrDisputeNoCharge
	}

	refund, err := s.refunder.RefundInTx(ctx, tx, billing.RefundRequest{
		TenantID:       dispute.TenantID,
		ClaimID:        claimID,
		AmountMicroUSD: amountMicroUSD,
		Reason:         "cost_dispute",
		AuditRequestID: disputeRefundAuditRequestID(dispute.DisputeID),
	})
	if err != nil {
		return nil, fmt.Errorf("audit: refund resolved dispute: %w", err)
	}
	if refund == nil {
		return nil, errors.New("audit: refund resolved dispute returned nil result")
	}
	if refund.RefundMicroUSD <= 0 && !refund.Idempotent {
		return nil, ErrDisputeNoCharge
	}
	return refund, nil
}

func disputeRefundAuditRequestID(disputeID string) string {
	return "dispute-" + strings.TrimSpace(disputeID)
}

// disputeCostUSDToMicros 与计费包私有换算保持相同的舍入、负数和溢出边界。
func disputeCostUSDToMicros(cost decimal.Decimal) (int64, error) {
	micros := cost.Mul(decimal.NewFromInt(1_000_000)).Round(0)
	if micros.IsNegative() {
		return 0, nil
	}
	if micros.GreaterThan(maxDisputeCostMicroUSDDecimal) {
		return 0, billing.ErrCostOverflow
	}
	return micros.IntPart(), nil
}
