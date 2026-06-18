// HUAKAI · iKun

package subscriptionenforce

import (
	"sort"
	"testing"
)

func keysOf(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sameSet(a map[int64]struct{}, want ...int64) bool {
	if len(a) != len(want) {
		return false
	}
	for _, w := range want {
		if _, ok := a[w]; !ok {
			return false
		}
	}
	return true
}

// 守方向 = 最小值优先: 两档命中(5→100, 20→200)只保留最小档的池。
// mutation: 把 m.priority < best 改成 >(取最大档) → 结果变 {200} → 红(方向写反必被抓)。
func TestHighestPriorityAllowed_TakesMinTier(t *testing.T) {
	got := highestPriorityAllowed([]matchedRoute{
		{poolGroupID: 100, priority: 5},
		{poolGroupID: 200, priority: 20},
	})
	if !sameSet(got, 100) {
		t.Fatalf("min-tier arbitration = %v, want {100} (priority 5 < 20)", keysOf(got))
	}
}

// 守 slice B 核心(收窄+变异方向显式): 三条命中横跨两档(50→100, 50→101, 100→200) → 只放最高档
// {100,101}, 低档 200 被收掉。比 TakesMinTier(2 条)更强: 同时压住"方向(取最小档)"与"同档并集"
// 与"低档丢弃"三件事, 且 priority 值非 0/100 边界, 避免与默认值巧合。
// mutation: 方向反(取 100 档)→{200} 红; 漏并集→{100} 红; 不丢低档→{100,101,200} 红。
func TestHighestPriorityAllowed_NarrowsToMinTierWithVariance(t *testing.T) {
	got := highestPriorityAllowed([]matchedRoute{
		{poolGroupID: 100, priority: 50},
		{poolGroupID: 101, priority: 50},
		{poolGroupID: 200, priority: 100},
	})
	if !sameSet(got, 100, 101) {
		t.Fatalf("narrow-with-variance = %v, want {100,101} (top tier 50; tier 100 dropped)", keysOf(got))
	}
}

// 守并列同档取并集: 三条命中(5→100, 5→101, 20→200)→ 最高档两池都保留。
// mutation: 把 == best 分支删掉(只留首条同档) → 漏掉 101 → 红。
func TestHighestPriorityAllowed_TiesUnion(t *testing.T) {
	got := highestPriorityAllowed([]matchedRoute{
		{poolGroupID: 100, priority: 5},
		{poolGroupID: 101, priority: 5},
		{poolGroupID: 200, priority: 20},
	})
	if !sameSet(got, 100, 101) {
		t.Fatalf("tie union = %v, want {100,101} (both at top tier 5)", keysOf(got))
	}
}

// 守向后兼容(关键): 全部默认优先级(同值)的多条命中 → 与旧"全量命中池"逐元素相等, 零行为变化。
// mutation: 任何把同档收成子集的实现 → 少一个池 → 红(证明默认配置不被收窄)。
func TestHighestPriorityAllowed_AllDefaultEquivalentToFullSet(t *testing.T) {
	matched := []matchedRoute{
		{poolGroupID: 100, priority: 100},
		{poolGroupID: 101, priority: 100},
		{poolGroupID: 102, priority: 100},
	}
	got := highestPriorityAllowed(matched)
	// 旧逻辑全量集 = 全部命中池。
	if !sameSet(got, 100, 101, 102) {
		t.Fatalf("all-default arbitration = %v, want full set {100,101,102} (backward compat)", keysOf(got))
	}
}

// 守 S0 硬不变量: 命中集非空 ⇒ 结果非空。任意非空输入都不得收成空。
// mutation: 把比较写成恒不命中(如 best 初始化错 + 永远走 > 分支丢弃) → 空集 → 红。
func TestHighestPriorityAllowed_NeverEmptyOnNonEmptyHits(t *testing.T) {
	cases := [][]matchedRoute{
		{{poolGroupID: 7, priority: 0}},                                    // 单条, priority=0(合法下界)
		{{poolGroupID: 7, priority: 100}, {poolGroupID: 8, priority: 100}}, // 同档
		{{poolGroupID: 7, priority: 30}, {poolGroupID: 8, priority: 5}},    // 乱序不同档
	}
	for i, c := range cases {
		if got := highestPriorityAllowed(c); len(got) == 0 {
			t.Fatalf("case %d: non-empty hits must yield non-empty Allowed, got empty", i)
		}
	}
}

// 守无命中 → 空集(非 nil): 与"已配置但本 model 未命中→拒"的白名单语义对齐。
// mutation: 返回 nil 或塞入哨兵 → len!=0 或 nil 解引用 → 红。
func TestHighestPriorityAllowed_EmptyOnNoHits(t *testing.T) {
	got := highestPriorityAllowed(nil)
	if got == nil {
		t.Fatal("empty hits must return non-nil empty map (not nil)")
	}
	if len(got) != 0 {
		t.Fatalf("empty hits Allowed = %v, want {}", keysOf(got))
	}
}

// 守顺序无关: 同一组命中无论输入顺序结果相同(防 reset-on-new-best 漏并集)。
// mutation: reset 分支误清空已收集的同档(用赋值替换而非按档重置错误) → 某顺序下少池 → 红。
func TestHighestPriorityAllowed_OrderIndependent(t *testing.T) {
	a := highestPriorityAllowed([]matchedRoute{
		{poolGroupID: 200, priority: 20},
		{poolGroupID: 100, priority: 5},
		{poolGroupID: 101, priority: 5},
	})
	b := highestPriorityAllowed([]matchedRoute{
		{poolGroupID: 100, priority: 5},
		{poolGroupID: 200, priority: 20},
		{poolGroupID: 101, priority: 5},
	})
	if !sameSet(a, 100, 101) || !sameSet(b, 100, 101) {
		t.Fatalf("order-dependent result: a=%v b=%v, want both {100,101}", keysOf(a), keysOf(b))
	}
}
