package alerting

import (
	"context"
	"sort"
	"strings"
	"time"
)

// EvaluateRulesFromSource 为同一租户按规则窗口复用指标快照。不同窗口各取一次，
// 相同窗口共享结果；旧数据源未实现窗口接口时仍只调用一次 Snapshot。
func (s *Service) EvaluateRulesFromSource(ctx context.Context, tenantID int64, source MetricSource) error {
	if source == nil {
		return ErrStoreNotConfigured
	}
	windowSnapshots := make(map[time.Duration]map[string]float64)
	dimensionSnapshots := make(map[string]map[string]float64)
	var fallbackSnapshot map[string]float64
	var fallbackLoaded bool

	return s.evaluateRules(ctx, tenantID, func(rule AlertRule) (map[string]float64, error) {
		window := time.Duration(rule.WindowSeconds) * time.Second
		dimensions := normalizeStringMap(rule.Filters)
		if len(dimensions) > 0 {
			if scoped, ok := source.(WindowedDimensionMetricSource); ok {
				cacheKey := windowedDimensionsKey(window, dimensions)
				if snapshot, exists := dimensionSnapshots[cacheKey]; exists {
					return snapshot, nil
				}
				snapshot, err := scoped.SnapshotWindowForDimensions(ctx, tenantID, dimensions, window)
				if err != nil {
					return nil, err
				}
				dimensionSnapshots[cacheKey] = snapshot
				return snapshot, nil
			}
			if scoped, ok := source.(DimensionMetricSource); ok {
				cacheKey := windowedDimensionsKey(0, dimensions)
				if snapshot, exists := dimensionSnapshots[cacheKey]; exists {
					return snapshot, nil
				}
				snapshot, err := scoped.SnapshotForDimensions(ctx, tenantID, dimensions)
				if err != nil {
					return nil, err
				}
				dimensionSnapshots[cacheKey] = snapshot
				return snapshot, nil
			}
		}
		if windowed, ok := source.(WindowedMetricSource); ok {
			if snapshot, exists := windowSnapshots[window]; exists {
				return snapshot, nil
			}
			snapshot, err := windowed.SnapshotWindow(ctx, tenantID, window)
			if err != nil {
				return nil, err
			}
			windowSnapshots[window] = snapshot
			return snapshot, nil
		}
		if fallbackLoaded {
			return fallbackSnapshot, nil
		}
		snapshot, err := source.Snapshot(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		fallbackSnapshot = snapshot
		fallbackLoaded = true
		return fallbackSnapshot, nil
	})
}

func windowedDimensionsKey(window time.Duration, dimensions map[string]string) string {
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(window.String())
	for _, key := range keys {
		b.WriteByte('\x00')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(dimensions[key])
	}
	return b.String()
}
