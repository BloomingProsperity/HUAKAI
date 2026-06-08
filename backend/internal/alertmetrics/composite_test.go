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
