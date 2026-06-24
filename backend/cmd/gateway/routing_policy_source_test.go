// routing_policy_source_test.go — 路由加权激活闭环测试(A 点亮 / B 默认不翻转 / C 注入真实)。
//
// 这些测试用生产 RoutingPolicySource(newBindingRoutingPolicySource)接真 DefaultSelector,
// 经公开 Select 路径驱动,断言 req.SelectionMode 真影响选号:
//   - "priority_weighted" → 按账号 Weight 加权,高 weight 账号显著更易被选(测试 A)。
//   - ""/"strict_priority" → 均匀,高 weight 账号不被偏向,与未接 policy source 时一致(测试 B)。
//   - GetRoutingPolicy 对 priority_weighted 返回非 nil 且模式正确(测试 C)。
package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

// weightedFakeAccountSource 返回三个同优先级、同负载、同 LastUsedAt 的账号(故落在同一 tie-band),
// 唯一差异是 Weight,用于观察加权选号是否真按 Weight 倾斜。
type weightedFakeAccountSource struct {
	accounts []*poolrouter.AccountSnapshot
}

func (s *weightedFakeAccountSource) ListAccounts(context.Context, poolrouter.SelectionRequest) ([]*poolrouter.AccountSnapshot, error) {
	// 返回副本指针切片(Select 内部可能重排,避免跨调用污染)。
	out := make([]*poolrouter.AccountSnapshot, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

// alwaysAcquireSlotManager 让每个候选都能拿到 slot,故 Select 必返回 rank 后的首选账号——
// 把"选号策略"从"slot 抢占"中隔离出来,使统计断言只反映加权/均匀逻辑本身。
type alwaysAcquireSlotManager struct{}

func (alwaysAcquireSlotManager) Acquire(context.Context, *poolrouter.AccountSnapshot, poolrouter.SelectionRequest) (*poolrouter.AcquireResult, error) {
	return &poolrouter.AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func sameBandWeightedAccounts() []*poolrouter.AccountSnapshot {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	mk := func(id int64, w int32) *poolrouter.AccountSnapshot {
		return &poolrouter.AccountSnapshot{
			ID: id, TenantID: 1, Priority: 100, LoadRate: 0.2, LastUsedAt: now, Weight: w,
		}
	}
	// 账号 10 重(weight=10),11/12 轻(weight=1)。
	return []*poolrouter.AccountSnapshot{mk(10, 10), mk(11, 1), mk(12, 1)}
}

// runSelectDraws 用给定 selector 跑 draws 次 Select,返回各账号被选计数。
func runSelectDraws(t *testing.T, sel *poolrouter.DefaultSelector, mode string, draws int) map[int64]int {
	t.Helper()
	counts := map[int64]int{}
	for i := 0; i < draws; i++ {
		res, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
			TenantID:       1,
			RequestedModel: "x",
			SelectionMode:  mode,
		})
		if err != nil {
			t.Fatalf("draw %d Select 失败: %v", i, err)
		}
		if res == nil || res.AccountID == 0 {
			t.Fatalf("draw %d 无选中账号: %+v", i, res)
		}
		counts[res.AccountID]++
	}
	return counts
}

// newWeightedSelector 构造接生产 policy source 的 selector(测试 A/C 走加权路径用)。
func newWeightedSelector(withPolicySource bool) *poolrouter.DefaultSelector {
	opts := []poolrouter.SelectorOption{
		poolrouter.WithSlotManager(alwaysAcquireSlotManager{}),
	}
	if withPolicySource {
		opts = append(opts, poolrouter.WithRoutingPolicySource(newBindingRoutingPolicySource()))
	}
	return poolrouter.NewDefaultSelector(
		&weightedFakeAccountSource{accounts: sameBandWeightedAccounts()},
		opts...,
	)
}

// TestRoutingWeightActivation_PriorityWeightedSkewsByWeight —— 测试 A:闭环点亮。
// binding=priority_weighted + 账号不同 Weight → 高 weight 账号被选概率显著更高。
// MUTATION:把 dispatch 穿线改回丢 selection_mode(等价于此处不传 priority_weighted)→ 退回均匀
// (见测试 B 的 ratio≈1)→ 本测试的 ratio≈10 断言红。
func TestRoutingWeightActivation_PriorityWeightedSkewsByWeight(t *testing.T) {
	sel := newWeightedSelector(true)
	const draws = 18000
	counts := runSelectDraws(t, sel, string(poolrouter.SelectionModePriorityWeighted), draws)

	lightAvg := float64(counts[11]+counts[12]) / 2
	if lightAvg == 0 {
		t.Fatalf("轻账号从未被选,counts=%v", counts)
	}
	ratio := float64(counts[10]) / lightAvg
	// 权重比 10:1,期望重账号被选频次约为轻账号均值的 10 倍。给宽容窗防抽样噪声。
	if ratio < 7.5 || ratio > 13.0 {
		t.Fatalf("加权选号 重:轻 比 = %.2f, 期望约 10; counts=%v", ratio, counts)
	}
}

// TestRoutingWeightActivation_DefaultStrictNotFlipped —— 测试 B:默认不翻转(self-proving)。
// binding 默认(SelectionMode="")→ 走均匀 Shuffle,高 weight 账号【不】被偏向;
// 且与"未接 RoutingPolicySource"时分布同性质(均匀)。self-proving:同一组账号,
// 接 policy source 的 strict 路径 与 不接 policy source 的路径,重账号占比都接近 1/3(均匀),
// 二者差异在抽样噪声内——证明接线【没有】改变默认行为。
// MUTATION:让 strict 也走加权(例如 GetRoutingPolicy 恒返回 priority_weighted)→ 重账号占比
// 跳到 ~10/12 ≈ 0.83 → 下面两个 near-uniform 断言齐红。
func TestRoutingWeightActivation_DefaultStrictNotFlipped(t *testing.T) {
	const draws = 18000

	// 接生产 policy source,但 mode 留空(默认 strict)。
	wired := newWeightedSelector(true)
	wiredCounts := runSelectDraws(t, wired, "", draws)

	// 完全不接 policy source(断点1 原状,policy() 恒 nil)。
	unwired := newWeightedSelector(false)
	unwiredCounts := runSelectDraws(t, unwired, "", draws)

	heavyShareWired := float64(wiredCounts[10]) / float64(draws)
	heavyShareUnwired := float64(unwiredCounts[10]) / float64(draws)

	// 三账号均匀时,重账号占比应约 1/3,绝不接近加权时的 ~0.83。
	if heavyShareWired < 0.28 || heavyShareWired > 0.39 {
		t.Fatalf("strict 接线后 重账号占比 = %.3f, 期望约 0.333(均匀);counts=%v", heavyShareWired, wiredCounts)
	}
	if heavyShareUnwired < 0.28 || heavyShareUnwired > 0.39 {
		t.Fatalf("未接 policy source 重账号占比 = %.3f, 期望约 0.333(均匀);counts=%v", heavyShareUnwired, unwiredCounts)
	}
	// self-proving:接线前后差异须在抽样噪声内(同为均匀分布,不应有系统性偏移)。
	delta := heavyShareWired - heavyShareUnwired
	if delta < -0.04 || delta > 0.04 {
		t.Fatalf("接线前后 重账号占比差 = %.3f 超噪声窗,默认行为被改变了;wired=%v unwired=%v", delta, wiredCounts, unwiredCounts)
	}
}

// TestBindingRoutingPolicySource_ReturnsNonNilByMode —— 测试 C:注入真实。
// 生产 RoutingPolicySource 对 priority_weighted 的请求返回非 nil 且 SelectionMode 正确;
// 对默认/strict 返回 strict_priority(非 priority_weighted)。
// MUTATION:若注入点漏接(回到断点1,policy() 恒 nil)→ 加权分支不可达 → 测试 A 红;
// 若本源恒返回 strict → 测试 A 红。本测试直接钉死映射本身。
func TestBindingRoutingPolicySource_ReturnsNonNilByMode(t *testing.T) {
	src := newBindingRoutingPolicySource()

	weighted, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{
		SelectionMode: string(poolrouter.SelectionModePriorityWeighted),
	})
	if err != nil {
		t.Fatalf("priority_weighted GetRoutingPolicy 失败: %v", err)
	}
	if weighted == nil {
		t.Fatalf("priority_weighted policy 为 nil(注入失效,加权分支永不可达)")
	}
	if weighted.SelectionMode != poolrouter.SelectionModePriorityWeighted {
		t.Fatalf("priority_weighted policy.SelectionMode = %q, 期望 priority_weighted", weighted.SelectionMode)
	}

	for _, mode := range []string{"", "strict_priority", "garbage_unknown"} {
		strict, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{SelectionMode: mode})
		if err != nil {
			t.Fatalf("mode=%q GetRoutingPolicy 失败: %v", mode, err)
		}
		if strict == nil {
			t.Fatalf("mode=%q policy 为 nil", mode)
		}
		if strict.SelectionMode == poolrouter.SelectionModePriorityWeighted {
			t.Fatalf("mode=%q 误判为 priority_weighted, 默认不该走加权", mode)
		}
	}
}
