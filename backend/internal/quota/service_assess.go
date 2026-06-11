package quota

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// service_assess.go — 配额 reserve 的逐策略评估:assessPolicy 按 metric 算
// 当前用量/申请量/是否超限,policyWindowForUpdate 取窗口行(FOR UPDATE 锁)。
// 从 service.go 拆出(软预算门:单文件 ≤600 行 / 超基线 5% 余量即拆子文件)。

type policyAssessment struct {
	window       WindowCounter
	current      decimal.Decimal
	amount       decimal.Decimal
	limit        decimal.Decimal
	exceeded     bool
	skipped      bool
	retryAfter   time.Duration
	requestCount int64
}

func assessPolicy(ctx context.Context, store PGStore, req ReserveRequest, policy Policy) (policyAssessment, error) {
	switch policy.Metric {
	case MetricRequests:
		counter, err := policyWindowForUpdate(ctx, store, req, policy)
		if err != nil {
			return policyAssessment{}, err
		}
		policy.Window = counter.Window
		limit := policy.LimitValue
		current := counter.ReservedValue.Add(counter.SettledValue)
		amount := decimal.NewFromInt(1)
		return policyAssessment{
			window:       counter,
			current:      current,
			amount:       amount,
			limit:        limit,
			exceeded:     current.Add(amount).GreaterThan(limit),
			retryAfter:   retryAfter(req.At, policy),
			requestCount: counter.RequestCount,
		}, nil
	case MetricCostUSD:
		counter, err := policyWindowForUpdate(ctx, store, req, policy)
		if err != nil {
			return policyAssessment{}, err
		}
		policy.Window = counter.Window
		current := counter.ReservedValue.Add(counter.SettledValue)
		amount := req.PredictedCost
		return policyAssessment{
			window:     counter,
			current:    current,
			amount:     amount,
			limit:      policy.LimitValue,
			exceeded:   current.Add(amount).GreaterThan(policy.LimitValue),
			retryAfter: retryAfter(req.At, policy),
		}, nil
	case MetricTokensEstimated:
		// 零/缺失估算 → 跳过(不阻断热路径):token 估算只在有可估输入时才计。
		if req.ReservedTokens <= 0 {
			return policyAssessment{skipped: true}, nil
		}
		counter, err := policyWindowForUpdate(ctx, store, req, policy)
		if err != nil {
			return policyAssessment{}, err
		}
		policy.Window = counter.Window
		current := counter.ReservedValue.Add(counter.SettledValue)
		amount := decimal.NewFromInt(req.ReservedTokens)
		return policyAssessment{
			window:     counter,
			current:    current,
			amount:     amount,
			limit:      policy.LimitValue,
			exceeded:   current.Add(amount).GreaterThan(policy.LimitValue),
			retryAfter: retryAfter(req.At, policy),
		}, nil
	case MetricConcurrency:
		return policyAssessment{
			amount:   decimal.NewFromInt(1),
			limit:    policy.LimitValue,
			skipped:  false,
			exceeded: false,
		}, nil
	default:
		return policyAssessment{skipped: true}, nil
	}
}

func policyWindowForUpdate(ctx context.Context, store PGStore, req ReserveRequest, policy Policy) (WindowCounter, error) {
	resolvedWindow, err := serviceWindowForPolicy(policy, req.At)
	if err != nil {
		return WindowCounter{}, err
	}
	window, err := store.UpsertWindow(ctx, WindowUpsert{
		TenantID: req.TenantID,
		PolicyID: policy.ID,
		Window:   resolvedWindow,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	counter, err := store.GetWindowForUpdate(ctx, req.TenantID, window.ID)
	if err != nil {
		return WindowCounter{}, err
	}
	counter.Window = resolvedWindow
	return counter, nil
}
