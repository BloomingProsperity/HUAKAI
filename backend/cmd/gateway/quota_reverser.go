package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// quotaCostReverser 把退款金额适配为配额成本冲减。生产退款链使用事务内接口，让余额、
// 账单、退款事实、审计收据与配额在同一事务提交；非事务接口仅供兼容调用路径使用。
type quotaCostReverser struct {
	svc *quota.Service
}

var _ auditreceipt.QuotaReverser = quotaCostReverser{}
var _ auditreceipt.QuotaReverserInTx = quotaCostReverser{}

func (r quotaCostReverser) ReverseSettledCost(ctx context.Context, tenantID, claimID int64, amountMicroUSD int64) (auditreceipt.QuotaReverseResult, error) {
	if r.svc == nil || amountMicroUSD <= 0 {
		return auditreceipt.QuotaReverseResult{Skipped: true}, nil
	}
	result, err := r.svc.ReverseCost(ctx, quota.ReverseCostRequest{
		TenantID: tenantID,
		ClaimID:  claimID,
		Amount:   decimal.NewFromInt(amountMicroUSD).Div(decimal.NewFromInt(1_000_000)),
	})
	return auditreceipt.QuotaReverseResult{Skipped: result.Skipped}, err
}

func (r quotaCostReverser) ReverseSettledCostInTx(ctx context.Context, tx pgx.Tx, tenantID, claimID int64, amountMicroUSD int64) (auditreceipt.QuotaReverseResult, error) {
	if r.svc == nil || tx == nil || amountMicroUSD <= 0 {
		return auditreceipt.QuotaReverseResult{Skipped: true}, nil
	}
	result, err := r.svc.ReverseCostInTx(ctx, tx, quota.ReverseCostRequest{
		TenantID: tenantID,
		ClaimID:  claimID,
		Amount:   decimal.NewFromInt(amountMicroUSD).Div(decimal.NewFromInt(1_000_000)),
	})
	return auditreceipt.QuotaReverseResult{Skipped: result.Skipped}, err
}
