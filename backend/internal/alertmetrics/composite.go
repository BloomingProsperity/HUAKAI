package alertmetrics

import (
	"context"
	"log/slog"
	"time"
)

const DefaultRecentUsageWindow = 10 * time.Minute

const (
	// 告警规则可用的租户用量指标 key:
	// usage.request_count、usage.request_rate_per_minute、usage.success_count、
	// usage.success_rate、usage.error_count、usage.error_rate、usage.total_cost_usd、
	// usage.latency_p95_ms、usage.latency_p99_ms。
	MetricUsageRequestCount         = "usage.request_count"
	MetricUsageRequestRatePerMinute = "usage.request_rate_per_minute"
	MetricUsageSuccessCount         = "usage.success_count"
	MetricUsageSuccessRate          = "usage.success_rate"
	MetricUsageErrorCount           = "usage.error_count"
	MetricUsageErrorRate            = "usage.error_rate"
	MetricUsageTotalCostUSD         = "usage.total_cost_usd"
	// 最近已结算窗口内的 TTFT(首字节)延迟分位值,单位毫秒。
	// 暴露出来是为了让延迟 SLO 告警规则能在 p95/p99 劣化时触发
	// (OPS-002)——此前只有 success/error 率可作为告警依据。
	MetricUsageLatencyP95MS = "usage.latency_p95_ms"
	MetricUsageLatencyP99MS = "usage.latency_p99_ms"
)

type MetricSource interface {
	Snapshot(context.Context, int64) (map[string]float64, error)
}

type DimensionMetricSource interface {
	SnapshotForDimensions(context.Context, int64, map[string]string) (map[string]float64, error)
}

type RecentUsageRolluper interface {
	RecentUsageRollup(context.Context, int64, time.Time) (RecentUsageRollup, error)
}

type UsageStatsGate interface {
	UsageStatsEnabled(context.Context, int64) (bool, error)
}

type RecentUsageRollup struct {
	RequestCount int64
	SuccessCount int64
	ErrorCount   int64
	TotalCostUSD float64
	// LatencyP95MS/LatencyP99MS 是窗口内的 TTFT 分位值,单位毫秒;当窗口内
	// 没有任何请求记录到首字节时为 0。
	LatencyP95MS float64
	LatencyP99MS float64
}

type CompositeMetricSourceConfig struct {
	GlobalSource  MetricSource
	UsageRolluper RecentUsageRolluper
	UsageStats    UsageStatsGate
	// AccountHealth 可选;非 nil 时 snapshot 附带 account.unhealthy_count 族
	// (DM-14)。不受 UsageStats 开关门控——账号健康不是用量统计。
	AccountHealth AccountHealthCounter
	RecentWindow  time.Duration
	Now           func() time.Time
}

type CompositeMetricSource struct {
	globalSource  MetricSource
	usageRolluper RecentUsageRolluper
	usageStats    UsageStatsGate
	accountHealth AccountHealthCounter
	recentWindow  time.Duration
	now           func() time.Time
}

func NewCompositeMetricSource(cfg CompositeMetricSourceConfig) *CompositeMetricSource {
	if cfg.RecentWindow <= 0 {
		cfg.RecentWindow = DefaultRecentUsageWindow
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &CompositeMetricSource{
		globalSource:  cfg.GlobalSource,
		usageRolluper: cfg.UsageRolluper,
		usageStats:    cfg.UsageStats,
		accountHealth: cfg.AccountHealth,
		recentWindow:  cfg.RecentWindow,
		now:           cfg.Now,
	}
}

func (s *CompositeMetricSource) Snapshot(ctx context.Context, tenantID int64) (map[string]float64, error) {
	return s.snapshot(ctx, tenantID, nil, 0)
}

// SnapshotWindow 只用 window 改变 usage.* 指标的聚合区间；账号健康指标是
// 当前状态快照，与统计窗口无关。window 非正数时沿用启动配置的默认窗口。
func (s *CompositeMetricSource) SnapshotWindow(ctx context.Context, tenantID int64, window time.Duration) (map[string]float64, error) {
	return s.snapshot(ctx, tenantID, nil, window)
}

func (s *CompositeMetricSource) SnapshotForDimensions(ctx context.Context, tenantID int64, dimensions map[string]string) (map[string]float64, error) {
	return s.snapshot(ctx, tenantID, dimensions, 0)
}

// SnapshotWindowForDimensions 同时保留维度过滤与规则窗口语义。
func (s *CompositeMetricSource) SnapshotWindowForDimensions(ctx context.Context, tenantID int64, dimensions map[string]string, window time.Duration) (map[string]float64, error) {
	return s.snapshot(ctx, tenantID, dimensions, window)
}

func (s *CompositeMetricSource) snapshot(ctx context.Context, tenantID int64, dimensions map[string]string, window time.Duration) (map[string]float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := s.globalSnapshot(ctx, tenantID, dimensions)
	if err != nil {
		return nil, err
	}
	// DM-14:account-health overlay 在 usage 早退分支之前,不被
	// usageRolluper 缺席/UsageStats 关闭跳过。
	s.overlayAccountHealth(ctx, tenantID, snapshot)
	if s == nil || s.usageRolluper == nil || tenantID <= 0 {
		return snapshot, nil
	}
	if window <= 0 {
		window = s.recentWindow
	}
	if s.usageStats != nil {
		enabled, err := s.usageStats.UsageStatsEnabled(ctx, tenantID)
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "alert metrics usage stats setting read failed",
					"tenant_id", tenantID,
					"error", err.Error(),
				)
			}
		} else if !enabled {
			return snapshot, nil
		}
	}

	rollup, err := s.usageRolluper.RecentUsageRollup(ctx, tenantID, s.now().Add(-window))
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "alert metrics recent usage rollup failed",
				"tenant_id", tenantID,
				"window_seconds", int64(window.Seconds()),
				"error", err.Error(),
			)
		}
		return snapshot, nil
	}
	overlayUsageMetrics(snapshot, rollup, window)
	return snapshot, nil
}

func (s *CompositeMetricSource) overlayAccountHealth(ctx context.Context, tenantID int64, snapshot map[string]float64) {
	if s == nil || s.accountHealth == nil || tenantID <= 0 {
		return
	}
	counts, err := s.accountHealth.UnhealthyAccountCounts(ctx, tenantID)
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "alert metrics account health counts failed",
				"tenant_id", tenantID,
				"error", err.Error(),
			)
		}
		return
	}
	var total int64
	for state, count := range counts {
		if count < 0 {
			count = 0
		}
		snapshot[MetricAccountUnhealthyPrefix+state] = float64(count)
		total += count
	}
	// 空计数也写 total=0:告警规则需要持续有值才能从 firing 恢复。
	snapshot[MetricAccountUnhealthyCount] = float64(total)
}

func (s *CompositeMetricSource) globalSnapshot(ctx context.Context, tenantID int64, dimensions map[string]string) (map[string]float64, error) {
	if s == nil || s.globalSource == nil {
		return map[string]float64{}, nil
	}
	if len(dimensions) > 0 {
		if scoped, ok := s.globalSource.(DimensionMetricSource); ok {
			snapshot, err := scoped.SnapshotForDimensions(ctx, tenantID, cloneDimensions(dimensions))
			if err != nil {
				return nil, err
			}
			return cloneSnapshot(snapshot), nil
		}
	}
	snapshot, err := s.globalSource.Snapshot(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return cloneSnapshot(snapshot), nil
}

func overlayUsageMetrics(snapshot map[string]float64, rollup RecentUsageRollup, window time.Duration) {
	requestCount := nonNegative(rollup.RequestCount)
	successCount := nonNegative(rollup.SuccessCount)
	errorCount := nonNegative(rollup.ErrorCount)

	var successRate float64
	var errorRate float64
	if requestCount > 0 {
		successRate = float64(successCount) / float64(requestCount)
		errorRate = float64(errorCount) / float64(requestCount)
	}

	var requestRatePerMinute float64
	if window > 0 {
		requestRatePerMinute = float64(requestCount) / window.Minutes()
	}

	snapshot[MetricUsageRequestCount] = float64(requestCount)
	snapshot[MetricUsageRequestRatePerMinute] = requestRatePerMinute
	snapshot[MetricUsageSuccessCount] = float64(successCount)
	snapshot[MetricUsageSuccessRate] = successRate
	snapshot[MetricUsageErrorCount] = float64(errorCount)
	snapshot[MetricUsageErrorRate] = errorRate
	snapshot[MetricUsageTotalCostUSD] = rollup.TotalCostUSD
	snapshot[MetricUsageLatencyP95MS] = nonNegativeFloat(rollup.LatencyP95MS)
	snapshot[MetricUsageLatencyP99MS] = nonNegativeFloat(rollup.LatencyP99MS)
}

func nonNegativeFloat(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func cloneSnapshot(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneDimensions(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
