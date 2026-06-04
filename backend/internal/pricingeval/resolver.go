package pricingeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billingdsl"
)

var microUSDDivisor = decimal.NewFromInt(1_000_000)

type Usage struct {
	InputTokens           int64
	OutputTokens          int64
	CacheCreationTokens   int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	CacheReadTokens       int64
}

type FlatRateFallback struct {
	Input           decimal.Decimal
	Output          decimal.Decimal
	CacheCreation   decimal.Decimal
	CacheCreation5m decimal.Decimal
	CacheCreation1h decimal.Decimal
	CacheRead       decimal.Decimal
	Multiplier      decimal.Decimal
	GroupRatio      decimal.Decimal

	HasInput           bool
	HasOutput          bool
	HasCacheCreation   bool
	HasCacheCreation5m bool
	HasCacheCreation1h bool
	HasCacheRead       bool
}

type Result struct {
	Total                 decimal.Decimal
	CacheCreationCost     decimal.Decimal
	CacheReadCost         decimal.Decimal
	CostSnapshot          string
	PendingReconciliation bool
}

func Resolve(ctx context.Context, raw json.RawMessage, usage Usage, fallback FlatRateFallback, version string) (Result, error) {
	if !isTieredPricingData(raw) {
		flat, err := FlatCost(usage, fallback)
		if err != nil {
			return Result{}, err
		}
		observeFlatCharged()
		return flat, nil
	}

	flat, flatErr := FlatCost(usage, fallback)
	spec, err := billingdsl.ParsePricingExpression(raw)
	if err == nil && !hasTieredBucket(spec) {
		err = errors.New("tiered pricing expression has no tier buckets")
	}
	if err != nil {
		return failSoftToFlat(ctx, version, err, flat, flatErr)
	}
	eval, err := billingdsl.Evaluate(spec, evalInput(usage), fallback.billingdsl())
	if err != nil {
		return failSoftToFlat(ctx, version, err, flat, flatErr)
	}
	groupRatio, err := pricingGroupRatio(fallback.GroupRatio)
	if err != nil {
		return Result{}, err
	}
	observeTieredCharged()
	return Result{
		Total:             applyGroupRatio(eval.Total, groupRatio),
		CacheCreationCost: applyGroupRatio(eval.CacheCreationCost, groupRatio),
		CacheReadCost:     applyGroupRatio(eval.CacheReadCost, groupRatio),
		CostSnapshot:      snapshotWithGroupRatio(tieredSnapshot(version), groupRatio),
	}, nil
}

func FlatCost(usage Usage, rates FlatRateFallback) (Result, error) {
	totalMicros, err := tokenMicros(usage.InputTokens, rates.Input, rates.HasInput, "input")
	if err != nil {
		return Result{}, err
	}
	outputMicros, err := tokenMicros(usage.OutputTokens, rates.Output, rates.HasOutput, "output")
	if err != nil {
		return Result{}, err
	}
	totalMicros = totalMicros.Add(outputMicros)
	cacheCreationMicros, err := cacheCreationMicros(usage, rates)
	if err != nil {
		return Result{}, err
	}
	totalMicros = totalMicros.Add(cacheCreationMicros)
	cacheReadMicros, err := tokenMicros(usage.CacheReadTokens, rates.CacheRead, rates.HasCacheRead, "cache_read")
	if err != nil {
		return Result{}, err
	}
	totalMicros = totalMicros.Add(cacheReadMicros)
	multiplier, err := flatMultiplier(rates.Multiplier)
	if err != nil {
		return Result{}, err
	}
	groupRatio, err := pricingGroupRatio(rates.GroupRatio)
	if err != nil {
		return Result{}, err
	}
	totalCost := scaleMicros(totalMicros, multiplier)
	cacheCreationCost := scaleMicros(cacheCreationMicros, multiplier)
	cacheReadCost := scaleMicros(cacheReadMicros, multiplier)
	return Result{
		Total:             applyGroupRatio(totalCost, groupRatio),
		CacheCreationCost: applyGroupRatio(cacheCreationCost, groupRatio),
		CacheReadCost:     applyGroupRatio(cacheReadCost, groupRatio),
		CostSnapshot:      snapshotWithGroupRatio("flat", groupRatio),
	}, nil
}

func failSoftToFlat(ctx context.Context, version string, tierErr error, flat Result, flatErr error) (Result, error) {
	observeTieredFallback()
	slog.WarnContext(ctx, "billing tiered pricing failed soft to flat",
		"billing_policy_version", strings.TrimSpace(version),
		"error", tierErr,
	)
	if flatErr != nil {
		return Result{}, fmt.Errorf("pricingeval: tiered pricing failed and flat fallback unavailable: %w", flatErr)
	}
	observeFlatCharged()
	flat.PendingReconciliation = true
	flat.CostSnapshot = "flat"
	return flat, nil
}

func cacheCreationMicros(usage Usage, rates FlatRateFallback) (decimal.Decimal, error) {
	if usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0 {
		fiveMinute, err := cacheCreationTierMicros(usage.CacheCreation5mTokens, rates.CacheCreation5m, rates.HasCacheCreation5m, rates.CacheCreation, rates.HasCacheCreation, "cache_creation_5m")
		if err != nil {
			return decimal.Zero, err
		}
		oneHour, err := cacheCreationTierMicros(usage.CacheCreation1hTokens, rates.CacheCreation1h, rates.HasCacheCreation1h, rates.CacheCreation, rates.HasCacheCreation, "cache_creation_1h")
		if err != nil {
			return decimal.Zero, err
		}
		return fiveMinute.Add(oneHour), nil
	}
	return tokenMicros(usage.CacheCreationTokens, rates.CacheCreation, rates.HasCacheCreation, "cache_creation")
}

func cacheCreationTierMicros(tokens int64, tierRate decimal.Decimal, hasTierRate bool, fallbackRate decimal.Decimal, hasFallbackRate bool, bucket string) (decimal.Decimal, error) {
	if tokens <= 0 {
		return decimal.Zero, nil
	}
	if hasTierRate {
		return tokenMicros(tokens, tierRate, true, bucket)
	}
	return tokenMicros(tokens, fallbackRate, hasFallbackRate, bucket)
}

func tokenMicros(tokens int64, rate decimal.Decimal, hasRate bool, bucket string) (decimal.Decimal, error) {
	if tokens <= 0 {
		return decimal.Zero, nil
	}
	if !hasRate {
		return decimal.Zero, fmt.Errorf("pricingeval: %s rate missing", bucket)
	}
	if rate.IsNegative() {
		return decimal.Zero, fmt.Errorf("pricingeval: %s rate negative", bucket)
	}
	return decimal.NewFromInt(tokens).Mul(rate), nil
}

func flatMultiplier(multiplier decimal.Decimal) (decimal.Decimal, error) {
	if multiplier.IsNegative() || multiplier.IsZero() {
		return decimal.Zero, errors.New("pricingeval: rate vector model_multiplier must be positive")
	}
	return multiplier, nil
}

func pricingGroupRatio(ratio decimal.Decimal) (decimal.Decimal, error) {
	if ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	if ratio.IsNegative() {
		return decimal.Zero, errors.New("pricingeval: group_ratio must be positive")
	}
	return ratio, nil
}

func scaleMicros(micros decimal.Decimal, multiplier decimal.Decimal) decimal.Decimal {
	return micros.Mul(multiplier).Div(microUSDDivisor)
}

func applyGroupRatio(cost decimal.Decimal, ratio decimal.Decimal) decimal.Decimal {
	return cost.Mul(ratio)
}

func snapshotWithGroupRatio(snapshot string, ratio decimal.Decimal) string {
	if ratio.Equal(decimal.NewFromInt(1)) {
		return snapshot
	}
	return snapshot + ";group_ratio=" + ratio.String()
}
