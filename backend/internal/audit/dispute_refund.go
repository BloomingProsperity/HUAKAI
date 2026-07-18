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
	pool          *pgxpool.Pool
	refunder      DisputeRefundExecutor
	quotaReverser QuotaReverserInTx
}

type CostDisputeResolverOption func(*CostDisputeResolver)

func WithDisputeQuotaReverser(reverser QuotaReverserInTx) CostDisputeResolverOption {
	return func(resolver *CostDisputeResolver) {
		resolver.quotaReverser = reverser
	}
}

func NewCostDisputeResolver(pool *pgxpool.Pool, refunder DisputeRefundExecutor, opts ...CostDisputeResolverOption) (*CostDisputeResolver, error) {
	if pool == nil || refunder == nil {
		return nil, ErrDisputeResolverRequired
	}
	resolver := &CostDisputeResolver{pool: pool, refunder: refunder}
	for _, opt := range opts {
		if opt != nil {
			opt(resolver)
		}
	}
	return resolver, nil
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
	rows, err := tx.Query(ctx, `
SELECT id, COALESCE(actual_cost, 0)
FROM billing_ledger_claims
WHERE tenant_id = $1
  AND user_id = $2
  AND logical_request_id = $3
  AND status = 'committed'
ORDER BY id ASC
LIMIT 2
FOR UPDATE`, dispute.TenantID, dispute.UserID, dispute.RequestID)
	if err != nil {
		return nil, fmt.Errorf("audit: find committed dispute claim: %w", err)
	}
	defer rows.Close()
	matchCount := 0
	for rows.Next() {
		matchCount++
		if err := rows.Scan(&claimID, &actualCost); err != nil {
			return nil, fmt.Errorf("audit: scan committed dispute claim: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate committed dispute claims: %w", err)
	}
	switch matchCount {
	case 0:
		return nil, ErrDisputeNoCharge
	case 1:
		// 唯一命中才允许自动退款。
	default:
		return nil, ErrDisputeAmbiguousCharge
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
		IdempotencyKey: disputeRefundIdempotencyKey(dispute.DisputeID),
		AuditRequestID: disputeRefundAuditRequestID(dispute.DisputeID),
	})
	if err != nil {
		if errors.Is(err, billing.ErrRefundNoCapturedCharge) {
			return nil, ErrDisputeNoCharge
		}
		return nil, fmt.Errorf("audit: refund resolved dispute: %w", err)
	}
	if refund == nil {
		return nil, errors.New("audit: refund resolved dispute returned nil result")
	}
	if refund.RefundMicroUSD <= 0 && !refund.Idempotent && !refund.AlreadySatisfied {
		return nil, ErrDisputeNoCharge
	}
	if s.quotaReverser != nil && refund.RefundMicroUSD > 0 && !refund.Idempotent {
		if _, err := s.quotaReverser.ReverseSettledCostInTx(ctx, tx, dispute.TenantID, claimID, refund.RefundMicroUSD); err != nil {
			return nil, fmt.Errorf("audit: reverse quota for resolved dispute: %w", err)
		}
	}
	return refund, nil
}

func disputeRefundAuditRequestID(disputeID string) string {
	return "dispute-" + strings.TrimSpace(disputeID)
}

func disputeRefundIdempotencyKey(disputeID string) string {
	return "cost_dispute_refund:" + strings.TrimSpace(disputeID)
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
