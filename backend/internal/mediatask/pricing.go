package mediatask

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

func EstimateCents(ctx context.Context, cfg Config, taskType string) (int64, error) {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		return 0, fmt.Errorf("%w: task_type", ErrInvalidInput)
	}
	cents, ok := cfg.DefaultEstimatedCents[taskType]
	if !ok {
		return 0, fmt.Errorf("%w: missing default estimate for %s", ErrInvalidInput, taskType)
	}
	if cents < 0 {
		return 0, fmt.Errorf("%w: negative default estimate for %s", ErrInvalidInput, taskType)
	}
	result, err := pricingeval.Resolve(ctx, nil, pricingeval.Usage{
		BillableUnits: decimal.NewFromInt(1),
	}, pricingeval.FlatRateFallback{
		PerUnit:    decimal.NewFromInt(cents).Mul(decimal.NewFromInt(10_000)),
		Multiplier: decimal.NewFromInt(1),
		HasPerUnit: true,
	}, cfg.BillingPolicyVersion)
	if err != nil {
		return 0, err
	}
	return usdToCents(result.Total), nil
}

func centsToUSD(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

func usdToCents(cost decimal.Decimal) int64 {
	if cost.IsNegative() {
		return 0
	}
	return cost.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}
