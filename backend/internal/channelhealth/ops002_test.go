package channelhealth

import (
	"expvar"
	"testing"
	"time"
)

// TestOPS002_ProviderErrorAndDegradedCountersIncrement 是 OPS-002 的判别性测试。
// 它通过 channelhealth.Service 驱动真实的 error/degraded 状态机转换,
// 并断言 expvar "provider_health" map 的计数器在恰当时点自增。
//
// 变异:若移除 incProviderError() 或 incProviderDegraded() 任一者,
// 对应计数器会停在 0,本测试随之变红。
func TestOPS002_ProviderErrorAndDegradedCountersIncrement(t *testing.T) {
	// 强制让 expvar map 在本进程中完成初始化。
	m := getProviderHealthMetrics()
	if m == nil {
		t.Fatal("getProviderHealthMetrics() returned nil")
	}

	// 读取基线值(测试可能在进程内共享 expvar 状态运行)。
	baselineDegraded := readProviderHealthInt("degraded_total")
	baselineError := readProviderHealthInt("error_total")

	ctx, svc, _, clock := testService()
	key := testKey()

	// --- Degraded 转换 ---
	// upstream_5xx 首次命中 → StateDegraded(依据 upstream5xxDecision,首次越界
	// 将 active→degraded;第二次越界将 degraded→cooling_down)。
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("ApplySignal upstream5xx[%d]: %v", i, err)
		}
	}
	gotDegraded := readProviderHealthInt("degraded_total") - baselineDegraded
	if gotDegraded < 1 {
		t.Fatalf("OPS-002: degraded_total did not increment after upstream_5xx degraded transition; delta=%d", gotDegraded)
	}

	// --- Error(cooling_down)转换 ---
	// 在已 degraded 的状态下继续驱动 upstream_5xx 信号 → cooling_down。
	baselineError2 := readProviderHealthInt("error_total")
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("ApplySignal upstream5xx2[%d]: %v", i, err)
		}
	}
	gotError := readProviderHealthInt("error_total") - baselineError2
	if gotError < 1 {
		t.Fatalf("OPS-002: error_total did not increment after cooling_down transition; delta=%d", gotError)
	}

	_ = baselineError // 抑制未使用变量告警
}

// TestOPS002_ProviderHealthExpvarReadable 校验 expvar map 存在,且两个
// key 都可按名读取 —— 与 otelbridge.bridgeCounters() 用的是同一条读取路径。
//
// 变异:移除 getProviderHealthMetrics() 初始化 → map 为 nil → 读取返回 0 →
// 即便发生了真实转换,bridgeCounters() 桥接也只会发出 0。
func TestOPS002_ProviderHealthExpvarReadable(t *testing.T) {
	// 确保 map 已初始化。
	_ = getProviderHealthMetrics()

	for _, key := range []string{"error_total", "degraded_total"} {
		m, ok := expvar.Get("provider_health").(*expvar.Map)
		if !ok || m == nil {
			t.Fatalf("expvar 'provider_health' map not found after init; key=%s", key)
		}
		if m.Get(key) == nil {
			t.Fatalf("expvar 'provider_health'.%s key not registered", key)
		}
	}
}

func readProviderHealthInt(key string) int64 {
	m, ok := expvar.Get("provider_health").(*expvar.Map)
	if !ok || m == nil {
		return 0
	}
	v, ok := m.Get(key).(*expvar.Int)
	if !ok || v == nil {
		return 0
	}
	return v.Value()
}
