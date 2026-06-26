package otelbridge

import (
	"testing"
)

// TestOPS002_BridgeCountersContainProviderHealthMetrics 验证 bridgeCounters() 中
// 同时存在 huakai_provider_error_total 和 huakai_provider_degraded_total,
// 且它们的 read 函数返回的是来自 "provider_health" expvar map 的值。
//
// 变异:从 bridgeCounters() 删除任一条目 → found[name]=false → 变红。
func TestOPS002_BridgeCountersContainProviderHealthMetrics(t *testing.T) {
	want := map[string]bool{
		"huakai_provider_error_total":    false,
		"huakai_provider_degraded_total": false,
	}
	for _, bc := range bridgeCounters() {
		if _, ok := want[bc.name]; ok {
			want[bc.name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("OPS-002: bridgeCounters() missing %s", name)
		}
	}
}

// TestOPS002_BridgeReadsProviderHealthExpvar 验证 huakai_provider_error_total 的
// 桥接 read 函数确实从 expvar map 读取。
//
// 变异:把 read 接到别的 map 或硬编码为 0 → 即便 channelhealth 计数器已递增,
// 值仍保持为 0 → 变红。
func TestOPS002_BridgeReadsProviderHealthExpvar(t *testing.T) {
	// 把该 expvar map 条目设为一个已知的哨兵值。
	setExpvarMapInt(t, "provider_health", "error_total", 42)

	for _, bc := range bridgeCounters() {
		if bc.name == "huakai_provider_error_total" {
			if got := bc.read(); got != 42 {
				t.Fatalf("OPS-002: bridge read() for huakai_provider_error_total=%d; want 42 (expvar not wired)", got)
			}
			return
		}
	}
	t.Fatal("OPS-002: huakai_provider_error_total not found in bridgeCounters()")
}
