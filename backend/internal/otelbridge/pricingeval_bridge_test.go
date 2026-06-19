package otelbridge

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

// TestPricingTieredFallbackBridgedToAlertSnapshot 守护 F-BILL-001:计价解析器的
// 资金完整性柔性降级信号必须能到达告警规则快照,这样当租户被静默地按平价兜底费率
// 收费(而非其配置的阶梯费率)时,运维才能据此告警。
//
// 测试驱动的是真实资金路径 —— 先做 N 次纯平价解析(只推 flat 分母),再用一份畸形的
// 阶梯计价规格调用 pricingeval.Resolve 端到端触发柔性降级分支(同时 +1 fallback 与
// +1 flat 分母)。断言取的是围绕这些调用的增量(expvar 状态是进程全局的,且与其他测试
// 共享),以此证明桥接反映的是实时计数器而非静态值。
//
// 为什么要先做 N 次纯平价:让 flatDelta(=N+1)与 fallbackDelta(=1)数值分离。否则
// 两条路径都只 +1,把 flat 桥接的 read 与 fallback 桥接的 read 互换也察觉不到。
//
// 变异:从 bridgeCounters() 删除 huakai_billing_pricing_tiered_fallback_total 条目、
// 或把它的 read 指向错误的 expvar map/key(例如换成 flat key)-> 该 key 增量不再等于
// 期望值 -> 变红。flat-charged 分母条目同理(精确等 N+1 而非 >=1)。
func TestPricingTieredFallbackBridgedToAlertSnapshot(t *testing.T) {
	source := NewExpvarMetricSource()

	before, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot (before): %v", err)
	}

	// 先做 N 次纯平价(非阶梯)解析:非阶梯规格只递增 flat-charged 分母,绝不触碰
	// fallback 计数器。这样 flat 分母被推到 N,而 fallback 仍为 0,使两个计数器的增量
	// 数值彼此分离 —— 这是把"误把 flat 桥接读成 fallback 的 expvar key(或反之)"这类
	// key-swap 错误暴露出来的关键:若两条 Resolve 路径都只 +1,交换读 key 也察觉不到。
	const flatOnlyCalls = 2
	flatRates := pricingeval.FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}
	for i := 0; i < flatOnlyCalls; i++ {
		if _, err := pricingeval.Resolve(context.Background(), []byte(`{"pricing_model":"flat"}`), pricingeval.Usage{InputTokens: 150}, flatRates, "price-v-test"); err != nil {
			t.Fatalf("纯平价 Resolve[%d] 必须成功(只递增 flat 分母),却得到错误 %v", i, err)
		}
	}

	// 畸形的阶梯计价:标记为阶梯计价,但其各档区间是非单调的,
	// 所以求值失败,Resolve 柔性降级到平价兜底(并把该笔收费标记为待对账)。
	// 这是真正的错收触发条件,而非桩。
	raw := []byte(`{
		"pricing_model":"tiered",
		"input":[
			{"up_to_tokens":200,"rate_micro_usd":"100"},
			{"up_to_tokens":100,"rate_micro_usd":"300"}
		]
	}`)
	fallback := pricingeval.FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}

	got, err := pricingeval.Resolve(context.Background(), raw, pricingeval.Usage{InputTokens: 150}, fallback, "price-v-test")
	if err != nil {
		t.Fatalf("Resolve must fail soft to flat, got error %v", err)
	}
	if !got.PendingReconciliation {
		t.Fatalf("Resolve fixture did not take the fail-soft path (PendingReconciliation=false); fixture no longer drives the counter")
	}

	after, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot (after): %v", err)
	}

	const fallbackKey = "huakai_billing_pricing_tiered_fallback_total"
	if delta := after[fallbackKey] - before[fallbackKey]; delta != 1 {
		t.Fatalf("%s delta=%v want 1 (bridge entry missing or wired to wrong expvar key)", fallbackKey, delta)
	}

	// 柔性降级分支同样会按平价费率收费,所以 flat-charged 分母也必须变动。
	// 精确断言 flatDelta == flatOnlyCalls+1(N 次纯平价 + 1 次柔性降级),而不是宽松的
	// >=1:这样若 flat 桥接条目误读了 fallback 的 expvar key,它只会看到 fallback 的
	// 增量 1 ≠ N+1=3 而变红;反过来若 fallback 桥接误读 flat key,fallbackDelta 会是
	// N+1=3 ≠ 1 同样变红 —— 两个方向的 key-swap 都被钉死。
	const flatKey = "huakai_billing_pricing_flat_charged_total"
	if delta := after[flatKey] - before[flatKey]; delta != flatOnlyCalls+1 {
		t.Fatalf("%s delta=%v want %d(%d 次纯平价 + 1 次柔性降级;桥接条目缺失或读错 expvar key)", flatKey, delta, flatOnlyCalls+1, flatOnlyCalls)
	}
}

// TestPricingTieredChargedDenominatorBridged 守护成功路径的分母:一次有效的阶梯
// Resolve 会递增 tiered-charged 总数,桥接必须将其暴露出来,这样 fallback 比率的分母
// 才完整(fallback / (flat + tiered))。
//
// 变异:移除 huakai_billing_pricing_tiered_charged_total 条目(或用错 key)->
// 快照增量为 0 -> 变红。
func TestPricingTieredChargedDenominatorBridged(t *testing.T) {
	source := NewExpvarMetricSource()

	before, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot (before): %v", err)
	}

	raw := []byte(`{
		"pricing_model":"tiered",
		"input":[
			{"up_to_tokens":100,"rate_micro_usd":"100"},
			{"up_to_tokens":1000,"rate_micro_usd":"200"}
		]
	}`)
	fallback := pricingeval.FlatRateFallback{
		Input:      decimal.NewFromInt(1000),
		Multiplier: decimal.NewFromInt(1),
		HasInput:   true,
	}

	got, err := pricingeval.Resolve(context.Background(), raw, pricingeval.Usage{InputTokens: 150}, fallback, "price-v-test")
	if err != nil {
		t.Fatalf("Resolve valid tiered spec error: %v", err)
	}
	if got.PendingReconciliation {
		t.Fatalf("valid tiered fixture took fail-soft path; fixture no longer drives the tiered-charged counter")
	}

	after, err := source.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("Snapshot (after): %v", err)
	}

	const tieredKey = "huakai_billing_pricing_tiered_charged_total"
	if delta := after[tieredKey] - before[tieredKey]; delta != 1 {
		t.Fatalf("%s delta=%v want 1 (bridge entry missing or wired to wrong expvar key)", tieredKey, delta)
	}
}
