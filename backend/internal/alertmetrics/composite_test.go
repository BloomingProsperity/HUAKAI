package alertmetrics

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// MUTATION: ignore tenantID or drop the per-tenant overlay -> usage keys are absent or tenant-blind -> RED.
func TestCompositeMetricSourceOverlaysPerTenant(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	rolluper := &stubUsageRolluper{
		rollup: RecentUsageRollup{
			RequestCount: 10,
			SuccessCount: 6,
			ErrorCount:   4,
			TotalCostUSD: 1.25,
		},
	}
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource:  stubGlobalSource{snapshot: map[string]float64{"global.counter": 3}},
		UsageRolluper: rolluper,
		RecentWindow:  10 * time.Minute,
		Now:           func() time.Time { return now },
	})

	got, err := source.Snapshot(context.Background(), 42)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertFloat(t, got[MetricUsageRequestCount], 10)
	assertFloat(t, got[MetricUsageSuccessCount], 6)
	assertFloat(t, got[MetricUsageSuccessRate], 0.6)
	assertFloat(t, got[MetricUsageErrorCount], 4)
	assertFloat(t, got[MetricUsageErrorRate], 0.4)
	assertFloat(t, got[MetricUsageTotalCostUSD], 1.25)
	assertFloat(t, got[MetricUsageRequestRatePerMinute], 1)
}

// MUTATION: emit rollup.LatencyP99MS under the p95 key (or drop either emit) ->
// the p95 assertion sees 480.25 instead of 120.5 -> RED. The fixture keeps p95
// and p99 distinct so a swapped/duplicated key cannot pass.
func TestCompositeMetricSourceOverlaysLatencyPercentiles(t *testing.T) {
	now := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	rolluper := &stubUsageRolluper{
		rollup: RecentUsageRollup{
			RequestCount: 4,
			LatencyP95MS: 120.5,
			LatencyP99MS: 480.25,
		},
	}
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource:  stubGlobalSource{snapshot: map[string]float64{}},
		UsageRolluper: rolluper,
		RecentWindow:  10 * time.Minute,
		Now:           func() time.Time { return now },
	})

	got, err := source.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	assertFloat(t, got[MetricUsageLatencyP95MS], 120.5)
	assertFloat(t, got[MetricUsageLatencyP99MS], 480.25)

	// A negative latency (clock skew / bad sample) must be clamped to 0, not
	// emitted as a negative SLO metric. MUTATION: skip nonNegativeFloat -> -1 -> RED.
	rolluper.rollup = RecentUsageRollup{RequestCount: 1, LatencyP95MS: -1, LatencyP99MS: -2}
	got, err = source.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	assertFloat(t, got[MetricUsageLatencyP95MS], 0)
	assertFloat(t, got[MetricUsageLatencyP99MS], 0)
}

// MUTATION: drop the expvar/global delegate -> existing global keys disappear -> RED.
func TestCompositeMetricSourcePreservesGlobals(t *testing.T) {
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource: stubGlobalSource{
			snapshot: map[string]float64{
				"huakai_dispatch_mode_default_total": 7,
			},
		},
		UsageRolluper: &stubUsageRolluper{rollup: RecentUsageRollup{RequestCount: 1}},
		RecentWindow:  time.Minute,
		Now:           fixedNow,
	})

	got, err := source.Snapshot(context.Background(), 77)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got["huakai_dispatch_mode_default_total"] != 7 {
		t.Fatalf("global metric missing or changed: got %v", got["huakai_dispatch_mode_default_total"])
	}
}

// MUTATION: pass tenantID 0 or a constant to the rollup querier -> tenant-scoped call assertion fails -> RED.
func TestCompositeMetricSourceTenantScoped(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC)
	rolluper := &stubUsageRolluper{rollup: RecentUsageRollup{RequestCount: 1}}
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource:  stubGlobalSource{snapshot: map[string]float64{}},
		UsageRolluper: rolluper,
		RecentWindow:  15 * time.Minute,
		Now:           func() time.Time { return now },
	})

	if _, err := source.Snapshot(context.Background(), 998877); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(rolluper.calls) != 1 {
		t.Fatalf("rolluper calls = %d, want 1", len(rolluper.calls))
	}
	call := rolluper.calls[0]
	if call.tenantID != 998877 {
		t.Fatalf("rolluper tenantID = %d, want 998877", call.tenantID)
	}
	wantSince := now.Add(-15 * time.Minute)
	if !call.settledSince.Equal(wantSince) {
		t.Fatalf("settledSince = %s, want %s", call.settledSince, wantSince)
	}
}

// MUTATION: propagate the usage rollup DB error -> scheduler evaluation breaks instead of using globals -> RED.
func TestCompositeMetricSourceDBErrorFailsSoft(t *testing.T) {
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource: stubGlobalSource{
			snapshot: map[string]float64{"huakai_cache_read_total": 12},
		},
		UsageRolluper: &stubUsageRolluper{err: errors.New("db unavailable")},
		RecentWindow:  time.Minute,
		Now:           fixedNow,
	})

	got, err := source.Snapshot(context.Background(), 45)
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil fail-soft", err)
	}
	if got["huakai_cache_read_total"] != 12 {
		t.Fatalf("global metric missing after DB error: got %v", got["huakai_cache_read_total"])
	}
	if _, ok := got[MetricUsageRequestCount]; ok {
		t.Fatalf("usage overlay present after DB error: %v", got)
	}
}

// MUTATION: ignore UsageStatsEnabled=false -> usage rollup runs and emits usage keys -> RED.
func TestCompositeMetricSourceUsageStatsToggle(t *testing.T) {
	rolluper := &stubUsageRolluper{rollup: RecentUsageRollup{RequestCount: 9}}
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		GlobalSource:  stubGlobalSource{snapshot: map[string]float64{"global.counter": 3}},
		UsageRolluper: rolluper,
		UsageStats:    stubUsageStatsGate{enabled: false},
		RecentWindow:  time.Minute,
		Now:           fixedNow,
	})

	got, err := source.Snapshot(context.Background(), 42)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got["global.counter"] != 3 {
		t.Fatalf("global metric missing: %v", got)
	}
	if len(rolluper.calls) != 0 {
		t.Fatalf("usage rollup calls = %d, want 0 when usage stats disabled", len(rolluper.calls))
	}
	if _, ok := got[MetricUsageRequestCount]; ok {
		t.Fatalf("usage metric emitted while usage stats disabled: %v", got)
	}
}

type stubGlobalSource struct {
	snapshot map[string]float64
	err      error
}

func (s stubGlobalSource) Snapshot(context.Context, int64) (map[string]float64, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]float64, len(s.snapshot))
	for key, value := range s.snapshot {
		out[key] = value
	}
	return out, nil
}

type usageRollupCall struct {
	tenantID     int64
	settledSince time.Time
}

type stubUsageRolluper struct {
	rollup RecentUsageRollup
	err    error
	calls  []usageRollupCall
}

type stubUsageStatsGate struct {
	enabled bool
	err     error
}

func (s stubUsageStatsGate) UsageStatsEnabled(context.Context, int64) (bool, error) {
	if s.err != nil {
		return true, s.err
	}
	return s.enabled, nil
}

func (s *stubUsageRolluper) RecentUsageRollup(_ context.Context, tenantID int64, settledSince time.Time) (RecentUsageRollup, error) {
	s.calls = append(s.calls, usageRollupCall{tenantID: tenantID, settledSince: settledSince})
	if s.err != nil {
		return RecentUsageRollup{}, s.err
	}
	return s.rollup, nil
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("got %.8f, want %.8f", got, want)
	}
}

type stubAccountHealth struct {
	counts map[string]int64
	err    error
}

func (s stubAccountHealth) UnhealthyAccountCounts(context.Context, int64) (map[string]int64, error) {
	return s.counts, s.err
}

// MUTATION: snapshot 去掉 overlayAccountHealth 调用 → 红(DM-14:自动摘除
// 的账号对告警引擎不可见,告警体系盲区复发)。
func TestCompositeSnapshotOverlaysAccountHealth(t *testing.T) {
	src := NewCompositeMetricSource(CompositeMetricSourceConfig{
		AccountHealth: stubAccountHealth{counts: map[string]int64{"cooldown": 2, "throttled": 1}},
	})
	got, err := src.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got[MetricAccountUnhealthyCount] != 3 {
		t.Fatalf("total=%v want 3; snapshot=%v", got[MetricAccountUnhealthyCount], got)
	}
	if got["account.unhealthy_count.cooldown"] != 2 || got["account.unhealthy_count.throttled"] != 1 {
		t.Fatalf("per-state 缺失: %v", got)
	}

	// 空计数也要有 total=0(告警规则恢复需要持续有值)
	src = NewCompositeMetricSource(CompositeMetricSourceConfig{AccountHealth: stubAccountHealth{counts: map[string]int64{}}})
	got, err = src.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got[MetricAccountUnhealthyCount]; !ok || v != 0 {
		t.Fatalf("空计数应有 total=0: %v", got)
	}

	// counter 出错 → 降级缺席,Snapshot 不失败
	src = NewCompositeMetricSource(CompositeMetricSourceConfig{AccountHealth: stubAccountHealth{err: errors.New("db down")}})
	got, err = src.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[MetricAccountUnhealthyCount]; ok {
		t.Fatalf("出错时不应有半截指标: %v", got)
	}

	// tenantID<=0(全局快照)不查
	src = NewCompositeMetricSource(CompositeMetricSourceConfig{AccountHealth: stubAccountHealth{counts: map[string]int64{"cooldown": 9}}})
	got, _ = src.Snapshot(context.Background(), 0)
	if _, ok := got[MetricAccountUnhealthyCount]; ok {
		t.Fatalf("tenant 0 不应 overlay: %v", got)
	}
}
