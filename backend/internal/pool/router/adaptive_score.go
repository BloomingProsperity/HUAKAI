package router

import (
	"math"
	"time"
)

const (
	adaptiveSignalFreshness = 15 * time.Minute
	adaptiveQuotaFreshness  = 2 * time.Hour
)

func adaptiveContributions(account *AccountSnapshot, now time.Time) map[string]float64 {
	if account == nil {
		return map[string]float64{}
	}
	result := map[string]float64{"capacity_headroom": clamp01(1-account.LoadRate) * 0.30}
	if account.UpstreamCostRatio != nil && *account.UpstreamCostRatio > 0 {
		result["upstream_cost_efficiency"] = math.Max(-0.05, math.Min(0.05, -0.025*math.Log2(*account.UpstreamCostRatio)))
	}
	if freshAt(account.RoutingSignalObservedAt, adaptiveSignalFreshness, now) && account.RoutingSignalSampleCount > 0 {
		result["reliability"] = clamp01(1-account.ErrorEWMA) * 0.35
		if account.ResponseLatencyMSEWMA > 0 {
			result["response_speed"] = clamp01(1-account.ResponseLatencyMSEWMA/30000) * 0.10
		}
	}
	if freshAt(account.UpstreamQuotaObservedAt, adaptiveQuotaFreshness, now) && account.UpstreamQuotaState == "available" && account.UpstreamQuotaRemainingKnown {
		result["quota_headroom"] = clamp01(account.UpstreamQuotaRemaining/100) * 0.20
		if account.UpstreamQuotaRemaining < 20 && !account.UpstreamQuotaResetsAt.IsZero() {
			untilReset := account.UpstreamQuotaResetsAt.Sub(now)
			if untilReset > 0 && untilReset <= time.Hour {
				result["near_reset_recovery"] = (1 - float64(untilReset)/float64(time.Hour)) * 0.05
			}
		}
	}
	return result
}

func adaptiveScore(account *AccountSnapshot, now time.Time) float64 {
	total := 0.0
	for _, value := range adaptiveContributions(account, now) {
		total += value
	}
	return total
}

func stickyDegradationReason(account *AccountSnapshot, now time.Time) string {
	if account == nil {
		return "bound_account_missing"
	}
	if freshAt(account.UpstreamQuotaObservedAt, adaptiveQuotaFreshness, now) && account.UpstreamQuotaState == "exhausted" {
		return "upstream_quota_exhausted"
	}
	if freshAt(account.RoutingSignalObservedAt, adaptiveSignalFreshness, now) && account.RoutingSignalSampleCount >= 5 && account.ErrorEWMA >= 0.65 {
		return "recent_error_rate_high"
	}
	return ""
}

func freshAt(observedAt time.Time, maxAge time.Duration, now time.Time) bool {
	if observedAt.IsZero() || observedAt.After(now) {
		return false
	}
	return now.Sub(observedAt) <= maxAge
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0
	}
	if value >= 1 {
		return 1
	}
	return value
}
