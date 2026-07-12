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

// processStart 近似为进程启动时刻:本包在 gateway 启动时初始化,
// 因此运行时 uptime 仪表盘据此推算。
var processStart = time.Now()

type bridgeCounter struct {
	name        string
	description string
	read        func() int64
	gauge       bool
}

// RegisterBridge 将选定的一组 expvar 计数器导出为 OTel observable counter。
// 计数值在 scrape 回调内部读取,因此不会引入后台 goroutine,
// 也不会产生重复的计数器状态。
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
	type registeredGauge struct {
		instrument otelmetric.Int64ObservableGauge
		read       func() int64
	}
	registeredCounters := make([]registeredCounter, 0, len(specs))
	registeredGauges := make([]registeredGauge, 0, 2)
	observables := make([]otelmetric.Observable, 0, len(specs))
	for _, spec := range specs {
		if spec.gauge {
			instrument, err := meter.Int64ObservableGauge(
				spec.name,
				otelmetric.WithDescription(spec.description),
			)
			if err != nil {
				return fmt.Errorf("register observable gauge %s: %w", spec.name, err)
			}
			registeredGauges = append(registeredGauges, registeredGauge{
				instrument: instrument,
				read:       spec.read,
			})
			observables = append(observables, instrument)
			continue
		}
		instrument, err := meter.Int64ObservableCounter(
			spec.name,
			otelmetric.WithDescription(spec.description),
		)
		if err != nil {
			return fmt.Errorf("register observable counter %s: %w", spec.name, err)
		}
		registeredCounters = append(registeredCounters, registeredCounter{
			instrument: instrument,
			read:       spec.read,
		})
		observables = append(observables, instrument)
	}

	_, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		for _, counter := range registeredCounters {
			value := counter.read()
			if value < 0 {
				value = 0
			}
			observer.ObserveInt64(counter.instrument, value)
		}
		for _, gauge := range registeredGauges {
			value := gauge.read()
			if value < 0 {
				value = 0
			}
			observer.ObserveInt64(gauge.instrument, value)
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
		// 预算/限流强制执行的 fail-open:当 budget store 在
		// reserve/settle/release 上出错时,gateway 会放行该请求而非
		// 拒绝它。把这个计数器(与上面 group_policy_fail_open 同列)桥接出来,
		// 让运维能在强制执行被后端故障静默绕过时告警。
		{
			name:        "huakai_budget_failopen_total",
			description: "Budget/rate enforcement fail-open events (store error bypassed enforcement; request allowed).",
			read:        func() int64 { return readExpvarInt("budget_fail_open_total") },
		},
		// OPS-002:从 channelhealth.Service 状态迁移桥接出来的 provider 健康计数器。
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
		// OPS-003:按 lane 划分的 DLQ pending 深度仪表盘。
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
		{
			name:        "huakai_delivered_unsettled_count",
			description: "已交付但尚未闭合结算的持久恢复行数量。",
			read:        func() int64 { return readExpvarMapInt("dlq_depth", "delivered_unsettled_count") },
			gauge:       true,
		},
		{
			name:        "huakai_delivered_unsettled_age_seconds",
			description: "最老一条已交付未结算恢复行的滞留秒数。",
			read:        func() int64 { return readExpvarMapInt("dlq_depth", "delivered_unsettled_age_seconds") },
			gauge:       true,
		},
		// F-GW-003 第 2 阶段:实时进程运行时资源仪表盘,直接从 Go runtime 读取
		// (而非 expvar 支撑)。通过与上面计数器相同的快照路径桥接,这样运维就能用
		// 现有的 alert-rule CRUD 对 gateway 自身的资源占用设阈值告警 —— heap_alloc 作为
		// 内存泄漏预算,goroutines 作为 goroutine 泄漏信号,uptime 用于捕捉 crash-loop /
		// 重启。只有 heap 会读 MemStats(每次 scrape 一次 stop-the-world;复合快照会缓存它),
		// 因此开销可忽略不计。
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
		// F-BILL-001 资金完整性可观测性:当某租户的阶梯计价数据无法解析或求值时,
		// 计价解析器会从阶梯费率柔性降级到平价费率,静默地按平价兜底收费,并将该笔
		// 收费标记为待对账。这条柔性降级路径本就会递增一个 expvar 计数器,但它对
		// Prometheus/OTel 暴露面和告警规则快照都是不可见的。把这个 fallback 总数桥接出来
		// 作为首要可告警信号(与上面 group_policy / budget 的 fail-open 总数同列),让运维
		// 能对静默错收进行告警;同时桥接 flat-charged 和 tiered-charged 总数作为分母,
		// 这样就能算出 fallback 比率,而不只是一个绝对计数。
		{
			name:        "huakai_billing_pricing_tiered_fallback_total",
			description: "计价解析器柔性降级事件(阶梯计价无法解析/求值;按平价兜底收费,待对账)。",
			read:        func() int64 { return readExpvarMapInt("billing_pricing_eval", "tiered_fallback_total") },
		},
		{
			name:        "huakai_billing_pricing_flat_charged_total",
			description: "计价解析器按平价费率收费的请求数(阶梯降级比率的分母)。",
			read:        func() int64 { return readExpvarMapInt("billing_pricing_eval", "flat_charged_total") },
		},
		{
			name:        "huakai_billing_pricing_tiered_charged_total",
			description: "计价解析器按阶梯费率收费的请求数(阶梯降级比率的分母)。",
			read:        func() int64 { return readExpvarMapInt("billing_pricing_eval", "tiered_charged_total") },
		},
		// F-CACHE-001 激活可观测性:把按 (vendor,model) 打标签的 L2 响应缓存计数器
		// 聚合成扁平总数,这样在运维启用缓存之前/期间,缓存健康度就是可告警的
		// (命中率坍塌、容量压力)。hit/miss 是单调递增的;size_bytes 是一个仪表盘,
		// 桥接方式与上面的 dlq 深度仪表盘相同。
		{
			name:        "huakai_cache_l2_hit_total",
			description: "L2 response-cache hits, summed across vendor/model labels.",
			read:        func() int64 { return readExpvarMapSum("huakai_cache_l2_hit_total") },
		},
		{
			name:        "huakai_cache_l2_miss_total",
			description: "L2 response-cache misses, summed across vendor/model labels.",
			read:        func() int64 { return readExpvarMapSum("huakai_cache_l2_miss_total") },
		},
		{
			name:        "huakai_cache_l2_size_bytes",
			description: "L2 response-cache total bytes, summed across vendor/model labels.",
			read:        func() int64 { return readExpvarMapSum("huakai_cache_l2_size_bytes") },
		},
		// go↔rust 出口衔接可观测(A2):把 mimicry sidecar 拨号结果计数按 result 桥出来。
		// result 维度与 A1 出口边界日志的 phase/error_class、Rust sidecar tracing 的 phase
		// 同口径,运维能把 /metrics 的某个 result 计数直接对到日志/tracing 的同名阶段
		// (看关联产物:指标↔日志↔跨边界 tracing 三处同源)。出口成功率 = ok /(ok+其余);
		// 默认 fail-closed 下 dial/write/read/rejected 即出口拒服务。
		{
			name:        "huakai_egress_sidecar_dial_ok_total",
			description: "Egress sidecar dials that established a tunnel (success numerator).",
			read:        func() int64 { return readExpvarMapInt("egress_sidecar_dial_total", "ok") },
		},
		{
			name:        "huakai_egress_sidecar_dial_fail_total",
			description: "Egress sidecar dials that failed dialing the unix socket (sidecar_unavailable).",
			read:        func() int64 { return readExpvarMapInt("egress_sidecar_dial_total", "dial_fail") },
		},
		{
			name:        "huakai_egress_sidecar_write_fail_total",
			description: "Egress sidecar dials that failed writing the control frame (sidecar_unavailable).",
			read:        func() int64 { return readExpvarMapInt("egress_sidecar_dial_total", "write_fail") },
		},
		{
			name:        "huakai_egress_sidecar_read_fail_total",
			description: "Egress sidecar dials that failed reading the ack frame (sidecar_unavailable).",
			read:        func() int64 { return readExpvarMapInt("egress_sidecar_dial_total", "read_fail") },
		},
		{
			// 注:Rust sidecar 的负 ack 同时涵盖 profile 拒绝与上游/代理不可达(upstream_failed),
			// Go 侧当前不区分,统一记入本桶。描述保持中性,不断言"一定是 profile";按 error_class
			// 细分 upstream_failed 为独立 follow-up 切片(需 Go 解析 ack.Error + 对齐 Rust phase)。
			name:        "huakai_egress_sidecar_rejected_total",
			description: "Egress sidecar dials the sidecar negatively acked (profile rejection or upstream/proxy unreachable).",
			read:        func() int64 { return readExpvarMapInt("egress_sidecar_dial_total", "rejected") },
		},
		// 出口降级(sidecar 不可用→Go-native mimicry)总数,跨 reason_class 求和。仅在
		// SidecarFallbackEnabled=true 时非零;非零=出口指纹保真度降级正在发生,应告警。
		{
			name:        "huakai_egress_sidecar_fallback_total",
			description: "Egress sidecar fallbacks to Go-native mimicry (fingerprint-fidelity degraded), summed across reason classes.",
			read:        func() int64 { return readExpvarMapSum("egress_sidecar_fallback_total") },
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

// readExpvarMapSum 对一个带标签的 expvar.Map 中每个 *expvar.Int 条目求和 —— 用于
// 把按 (vendor,model) 打标签的缓存计数器折叠成一个扁平指标,供
// Prometheus 导出和扁平的 alert-rule 快照使用。
func readExpvarMapSum(name string) int64 {
	metricMap, ok := expvar.Get(name).(*expvar.Map)
	if !ok || metricMap == nil {
		return 0
	}
	var sum int64
	metricMap.Do(func(kv expvar.KeyValue) {
		if value, ok := kv.Value.(*expvar.Int); ok && value != nil {
			sum += value.Value()
		}
	})
	return sum
}
