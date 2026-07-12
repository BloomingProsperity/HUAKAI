package main

import (
	"context"

	"github.com/shopspring/decimal"

	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// quotaCostReverser 把 audit 退款 worker 的 micro USD 冲减请求适配到 quota.Service.ReverseCost。
// 它实现 auditreceipt.QuotaReverser:退款落库后,把退款的实际金额(micro USD)转成 USD,从配额
// settled_value 负向冲减。quota.Service 无状态(仅包一层 PG store),此处独立实例与配额强制层用的是
// 同一套 quota 表,功能等价。
type quotaCostReverser struct {
	svc *quota.Service
}

var _ auditreceipt.QuotaReverser = quotaCostReverser{}

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
