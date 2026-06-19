package otelbridge

import (
	"context"
	"expvar"
	"fmt"
	"runtime"
	"time"

	otelmetric "go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/BloomingProsperity/HUAKAI/internal/otelbridge"

// processStart approximates process boot time: this package initializes at gateway
// startup, so the runtime uptime gauge is derived from it.
var processStart = time.Now()

type bridgeCounter struct {
	name        string
	description string
	read        func() int64
}

// RegisterBridge exports selected expvar counters as OTel observable counters.
// Values are read inside the scrape callback, so no background goroutine or
// duplicate counter state is introduced.
func RegisterBridge(_ context.Context, mp otelmetric.MeterProvider) error {
	if mp == nil {
		return fmt.Errorf("nil meter provider")
	}
	meter := mp.Meter(meterName)
	specs := bridgeCounters()

	type registeredCounter struct {
		instrument otelmetric.Int64ObservableCounter
		read       func() int64
	}
	registered := make([]registeredCounter, 0, len(specs))
	observables := make([]otelmetric.Observable, 0, len(specs))
	for _, spec := range specs {
		instrument, err := meter.Int64ObservableCounter(
			spec.name,
			otelmetric.WithDescription(spec.description),
		)
		if err != nil {
			return fmt.Errorf("register observable counter %s: %w", spec.name, err)
		}
		registered = append(registered, registeredCounter{
			instrument: instrument,
			read:       spec.read,
		})
		observables = append(observables, instrument)
	}

	_, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		for _, counter := range registered {
			value := counter.read()
			if value < 0 {
				value = 0
			}
			observer.ObserveInt64(counter.instrument, value)
		}
		return nil
	}, observables...)
	if err != nil {
		return fmt.Errorf("register expvar bridge callback: %w", err)
	}
	return nil
}

type ExpvarMetricSource struct{}

func NewExpvarMetricSource() ExpvarMetricSource {
	return ExpvarMetricSource{}
}

func (ExpvarMetricSource) Snapshot(ctx context.Context, _ int64) (map[string]float64, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	specs := bridgeCounters()
	out := make(map[string]float64, len(specs))
	for _, spec := range specs {
		value := spec.read()
		if value < 0 {
			value = 0
		}
		out[spec.name] = float64(value)
	}
	return out, nil
}

func (s ExpvarMetricSource) SnapshotForDimensions(ctx context.Context, tenantID int64, _ map[string]string) (map[string]float64, error) {
	return s.Snapshot(ctx, tenantID)
}

func bridgeCounters() []bridgeCounter {
	return []bridgeCounter{
		{
			name:        "huakai_billing_resolver_db_fail_total",
			description: "Billing settings resolver database read failures.",
			read:        func() int64 { return readExpvarMapInt("billing_settings", "resolver_db_read_fail_total") },
		},
		{
			name:        "huakai_billing_resolver_stale_total",
			description: "Billing settings resolver stale-cache responses after refresh failure.",
			read:        func() int64 { return readExpvarMapInt("billing_settings", "resolver_stale_on_refresh_failure_total") },
		},
		{
			name:        "huakai_dispatch_mode_default_total",
			description: "PASR dispatcher requests using default mode.",
			read:        func() int64 { return readExpvarMapInt("pasr_dispatch", "mode_default_total") },
		},
		{
			name:        "huakai_dispatch_mode_shadow_total",
			description: "PASR dispatcher requests using shadow mode.",
			read:        func() int64 { return readExpvarMapInt("pasr_dispatch", "mode_shadow_total") },
		},
		{
			name:        "huakai_dispatch_mode_canary_total",
			description: "PASR dispatcher requests using canary mode.",
			read:        func() int64 { return readExpvarMapInt("pasr_dispatch", "mode_canary_total") },
		},
		{
			name:        "huakai_dispatch_mode_pasr_primary_total",
			description: "PASR dispatcher requests using primary PASR mode.",
			read:        func() int64 { return readExpvarMapInt("pasr_dispatch", "mode_pasr_primary_total") },
		},
		{
			name:        "huakai_dispatch_mode_pasr_strict_total",
			description: "PASR dispatcher requests using strict PASR mode.",
			read:        func() int64 { return readExpvarMapInt("pasr_dispatch", "mode_pasr_strict_total") },
		},
		{
			name:        "huakai_cache_creation_total",
			description: "Provider prompt cache creation token total.",
			read:        func() int64 { return readExpvarMapInt("cache_token_count", "creation_total") },
		},
		{
			name:        "huakai_cache_read_total",
			description: "Provider prompt cache read token total.",
			read:        func() int64 { return readExpvarMapInt("cache_token_count", "read_total") },
		},
		{
			name:        "huakai_group_policy_failopen_total",
			description: "Subscription group policy fail-open decisions.",
			read:        func() int64 { return readExpvarInt("group_policy_fail_open_total") },
		},
		{
			name:        "huakai_group_policy_failclosed_total",
			description: "Subscription group policy fail-closed decisions.",
			read:        func() int64 { return readExpvarInt("group_policy_fail_closed_total") },
		},
		// Budget/rate-limit enforcement fail-open: when the budget store errors on
		// reserve/settle/release, the gateway allows the request through rather than
		// reject it. Bridging this counter (peer of group_policy_fail_open above) lets
		// operators alert when enforcement is silently bypassed by backend failure.
		{
			name:        "huakai_budget_failopen_total",
			description: "Budget/rate enforcement fail-open events (store error bypassed enforcement; request allowed).",
			read:        func() int64 { return readExpvarInt("budget_fail_open_total") },
		},
		// OPS-002: provider health counters bridged from channelhealth.Service transitions.
		{
			name:        "huakai_provider_error_total",
			description: "Provider channel health error-rate or ban transitions (cooling_down / disabled).",
			read:        func() int64 { return readExpvarMapInt("provider_health", "error_total") },
		},
		{
			name:        "huakai_provider_degraded_total",
			description: "Provider channel health degraded transitions.",
			read:        func() int64 { return readExpvarMapInt("provider_health", "degraded_total") },
		},
		// OPS-003: DLQ pending depth gauges per lane.
		{
			name:        "huakai_dlq_pending_depth_high",
			description: "Pending DLQ rows in the HIGH lane.",
			read:        func() int64 { return readExpvarMapInt("dlq_depth", "depth_HIGH") },
		},
		{
			name:        "huakai_dlq_pending_depth_med",
			description: "Pending DLQ rows in the MED lane.",
			read:        func() int64 { return readExpvarMapInt("dlq_depth", "depth_MED") },
		},
		{
			name:        "huakai_dlq_pending_depth_low",
			description: "Pending DLQ rows in the LOW lane.",
			read:        func() int64 { return readExpvarMapInt("dlq_depth", "depth_LOW") },
		},
		// F-GW-003 Phase 2: live process runtime-resource gauges, read directly from the
		// Go runtime (not expvar-backed). Bridged through the same snapshot path as the
		// counters above so an operator can target the gateway's own footprint with the
		// existing alert-rule CRUD — heap_alloc as a memory-leak budget, goroutines as a
		// goroutine-leak signal, uptime to catch crash-loop / restart. Only heap reads
		// MemStats (one stop-the-world per scrape; the composite snapshot caches it), so
		// the cost stays negligible.
		{
			name:        "huakai_runtime_heap_alloc_bytes",
			description: "Live Go heap-allocated bytes (process memory-budget signal).",
			read: func() int64 {
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				return int64(ms.HeapAlloc)
			},
		},
		{
			name:        "huakai_runtime_goroutines",
			description: "Live goroutine count (goroutine-leak signal).",
			read:        func() int64 { return int64(runtime.NumGoroutine()) },
		},
		{
			name:        "huakai_runtime_uptime_seconds",
			description: "Process uptime in seconds (crash-loop / restart signal).",
			read:        func() int64 { return int64(time.Since(processStart).Seconds()) },
		},
	}
}

func readExpvarInt(name string) int64 {
	value, ok := expvar.Get(name).(*expvar.Int)
	if !ok || value == nil {
		return 0
	}
	return value.Value()
}

func readExpvarMapInt(mapName, key string) int64 {
	metricMap, ok := expvar.Get(mapName).(*expvar.Map)
	if !ok || metricMap == nil {
		return 0
	}
	value, ok := metricMap.Get(key).(*expvar.Int)
	if !ok || value == nil {
		return 0
	}
	return value.Value()
}
