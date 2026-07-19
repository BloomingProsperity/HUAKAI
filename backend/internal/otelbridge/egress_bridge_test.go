package otelbridge

import (
	"context"
	"expvar"
	"testing"

	// 空导入让 mimicry 的包级变量注册出口拨号指标。
	_ "github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// TestEgressSidecarBridgedToAlertSnapshot 守护 A2:go↔rust 出口衔接指标必须能到达告警快照。
// 出口是 relay 的最后一跳(Go→Rust sidecar→上游)，拨号失败必须可见并可告警。
//
// 逐个 result 桶灌入【互不相同】的量级,再断言每个 bridge 条目的快照增量精确等于其对应桶的量级。
// 这样一旦某条 bridge 把 read 指到错误的 expvar key(key-swap,例如 dial_fail 误读 ok 桶),
// 量级对不上即变红——这正是 pricingeval 桥接测试用"分母数值分离"钉 key-swap 的同款手法。
//
// 变异:从 bridgeCounters() 删除任一 huakai_egress_sidecar_* 条目,或把它的 read 指向错误的
// expvar map/key -> 对应快照增量 != 期望量级 -> 红。
func TestEgressSidecarBridgedToAlertSnapshot(t *testing.T) {
	dialMap, ok := expvar.Get("egress_sidecar_dial_total").(*expvar.Map)
	if !ok || dialMap == nil {
		t.Fatal("egress_sidecar_dial_total 未注册:mimicry 包级 expvar 未加载")
	}
	source := NewExpvarMetricSource()
	before, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot(before): %v", err)
	}

	// 每个 result 桶给一个互异量级,专门用于暴露 bridge 条目之间的 key-swap。
	type seed struct {
		metric  string // bridge 暴露的指标名
		expvarK string // 底层 expvar map 的 key
		bump    int64  // 本桶灌入量级(互异)
	}
	dialSeeds := []seed{
		{"huakai_egress_sidecar_dial_ok_total", "ok", 5},
		{"huakai_egress_sidecar_dial_fail_total", "dial_fail", 7},
		{"huakai_egress_sidecar_write_fail_total", "write_fail", 11},
		{"huakai_egress_sidecar_read_fail_total", "read_fail", 13},
		{"huakai_egress_sidecar_rejected_total", "rejected", 17},
	}
	for _, s := range dialSeeds {
		dialMap.Add(s.expvarK, s.bump)
	}
	after, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot(after): %v", err)
	}

	for _, s := range dialSeeds {
		if delta := int64(after[s.metric] - before[s.metric]); delta != s.bump {
			t.Fatalf("%s 增量=%d 应为 %d(bridge 条目缺失或读错 expvar key)", s.metric, delta, s.bump)
		}
	}
}
