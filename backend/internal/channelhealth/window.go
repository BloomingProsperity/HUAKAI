package channelhealth

import (
	"sort"
	"time"
)

func addSignalToWindow(w WindowSummary, p Policy, sig Signal, now time.Time) WindowSummary {
	if sig.At.IsZero() {
		sig.At = now
	}
	maxWindow := maxDuration(
		p.ErrorRateWindow,
		p.LatencyWindow,
		p.RateLimitWindow,
		p.Upstream5xxWindow,
		p.MinObservation,
	)
	cutoff := now.Add(-maxWindow)
	samples := make([]SignalSample, 0, len(w.Samples)+1)
	for _, sample := range w.Samples {
		if !sample.At.Before(cutoff) {
			samples = append(samples, sample)
		}
	}
	samples = append(samples, SignalSample{
		At:         sig.At.UTC(),
		Class:      normalizeSignalClass(sig.Class),
		StatusCode: sig.StatusCode,
		LatencyMS:  sig.LatencyMS,
	})
	return summarizeSamples(samples)
}

func summarizeSamples(samples []SignalSample) WindowSummary {
	out := WindowSummary{Samples: samples}
	var latencies []int64
	for i, sample := range samples {
		if i == 0 || sample.At.Before(out.WindowStartedAt) {
			out.WindowStartedAt = sample.At
		}
		if i == 0 || sample.At.After(out.WindowEndedAt) {
			out.WindowEndedAt = sample.At
		}
		out.TotalAttempts++
		switch normalizeSignalClass(sample.Class) {
		case SignalRateLimit:
			out.RateLimitHits++
			out.FailedAttempts++
		case SignalUpstream5xx:
			out.Upstream5xxHits++
			out.FailedAttempts++
		case SignalLocalGateway5xx:
			out.LocalGateway5xxHits++
		case SignalClientMalformed, SignalSuccess, SignalLatencyP99, SignalNone:
			// 客户端格式错误与本地网关故障不计入 channel health。
			// 延迟单独评估。
		default:
			if isBanSignal(sample.Class) {
				out.BanSignals++
				out.FailedAttempts++
			} else {
				out.FailedAttempts++
			}
		}
		if sample.LatencyMS > 0 {
			latencies = append(latencies, sample.LatencyMS)
		}
	}
	out.LatencyP99MS = percentile99(latencies)
	return out
}

func windowFor(w WindowSummary, d time.Duration, now time.Time) WindowSummary {
	if d <= 0 {
		return summarizeSamples(w.Samples)
	}
	cutoff := now.Add(-d)
	samples := make([]SignalSample, 0, len(w.Samples))
	for _, sample := range w.Samples {
		if !sample.At.Before(cutoff) {
			samples = append(samples, sample)
		}
	}
	return summarizeSamples(samples)
}

func rate(num, denom int) float64 {
	if denom <= 0 {
		return 0
	}
	return float64(num) * 100 / float64(denom)
}

func percentile99(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := int(float64(len(values))*0.99 + 0.5)
	if idx >= len(values) {
		idx = len(values) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return values[idx]
}

func maxDuration(values ...time.Duration) time.Duration {
	var out time.Duration
	for _, v := range values {
		if v > out {
			out = v
		}
	}
	return out
}

func normalizeSignalClass(class SignalClass) SignalClass {
	if class == "" {
		return SignalNone
	}
	return class
}

func isBanSignal(class SignalClass) bool {
	switch normalizeSignalClass(class) {
	case SignalAccountSuspended, SignalTokenRevoked, SignalCredentialRevoked,
		SignalAccountDisabled, SignalSubscriptionOrWorkspaceDisabled,
		SignalPolicyAutoDisabled:
		return true
	default:
		return false
	}
}
