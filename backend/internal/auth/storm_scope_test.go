package auth

import (
	"context"
	"testing"
	"time"
)

// fixedClockController 构建一个带 scope budget 的 controller, 其时钟取自
// 解引用的 *clk, 这样测试可以确定性地推进时间。
func fixedClockController(cfg StormScopeConfig, clk *time.Time) *StormController {
	c := NewStormControllerWithScopeBudget(nil, cfg)
	c.now = func() time.Time { return *clk }
	return c
}

// TestStormControllerEndpointScopeDeniesWhenBudgetExhausted: endpoint burst=2 时,
// 同一时刻对同一 endpoint 的第三次 acquire 被拒绝。变异: 让
// scopeBucket.tryAcquire 永远返回 true → 第三次 acquire 准入 → 红。
func TestStormControllerEndpointScopeDeniesWhenBudgetExhausted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 2}, &now)
	for i := 1; i <= 2; i++ {
		if _, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" || err != nil {
			t.Fatalf("acquire #%d: outcome=%q err=%v, want admit", i, outcome, err)
		}
	}
	refund, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", "")
	if err != nil || outcome != OutcomeStormBudgetExhausted || refund != nil {
		t.Fatalf("acquire #3: refund!=nil=%v outcome=%q err=%v, want storm_budget_exhausted denial", refund != nil, outcome, err)
	}
}

// TestStormControllerEndpointScopeRefillsOverTime: burst=1 rate=1/s。acquire,
// 立即被拒, 推进 1s, 再次 acquire。变异: 在 refillLocked 中跳过基于经过时间的
// 补充 → 推进后的 acquire 被拒 → 红。
func TestStormControllerEndpointScopeRefillsOverTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("first acquire denied: %q", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != OutcomeStormBudgetExhausted {
		t.Fatalf("immediate second acquire outcome=%q, want denial", outcome)
	}
	now = now.Add(time.Second)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("acquire after 1s refill outcome=%q, want admit", outcome)
	}
}

// TestStormControllerEndpointBucketsIndependentPerProvider: provider A 耗尽
// 不得拒绝 provider B。变异: 把每个 endpoint 都映射到同一个共享 bucket →
// provider B 被拒 → 红。
func TestStormControllerEndpointBucketsIndependentPerProvider(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("provider A first acquire denied: %q", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != OutcomeStormBudgetExhausted {
		t.Fatalf("provider A second acquire outcome=%q, want denial", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "openai", ""); outcome != "" {
		t.Fatalf("provider B acquire outcome=%q, want admit (independent per-endpoint bucket)", outcome)
	}
}

// TestStormControllerEndpointRefundReturnsToken: burst=1 时, 第一次 acquire
// 消费掉 token, refund 退回它, 下一次 acquire 准入。变异: 让
// scopeBucket.refund 变成 no-op → refund 后的 acquire 被拒 → 红。
func TestStormControllerEndpointRefundReturnsToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	refund, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", "")
	if outcome != "" || refund == nil {
		t.Fatalf("first acquire outcome=%q refund!=nil=%v, want admit", outcome, refund != nil)
	}
	refund()
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("acquire after refund outcome=%q, want admit (refund must return the token)", outcome)
	}
}

// TestStormControllerGlobalScopeDeniesWhenExhausted: global burst=1 准入一次
// 然后拒绝。变异: 在 AcquireGlobal 中跳过 global bucket 检查 → 第二次
// acquire 准入 → 红。
func TestStormControllerGlobalScopeDeniesWhenExhausted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{GlobalRate: 1, GlobalBurst: 1}, &now)
	if _, outcome, _ := c.AcquireGlobal(context.Background(), 1); outcome != "" {
		t.Fatalf("first global acquire denied: %q", outcome)
	}
	if _, outcome, err := c.AcquireGlobal(context.Background(), 1); outcome != OutcomeStormBudgetExhausted || err != nil {
		t.Fatalf("second global acquire outcome=%q err=%v, want denial", outcome, err)
	}
}

// TestStormControllerSubUnitBurstTreatedAsDisabled: burst < 1 永远无法准入一个
// 完整 token, 所以本层把该 scope 视作关闭 (admit-all), 而不是拒绝
// 每一次 refresh。变异: 放宽 endpointEnabled 接受 burst>0 → acquire 落入
// 一个 0.5-token 的 bucket 并被拒 → 红。
func TestStormControllerSubUnitBurstTreatedAsDisabled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 0.5}, &now)
	for i := 1; i <= 3; i++ {
		if _, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" || err != nil {
			t.Fatalf("acquire #%d with sub-unit burst: outcome=%q err=%v, want admit (scope disabled)", i, outcome, err)
		}
	}
}

// TestStormControllerNilSafeScopes 证明 nil controller 对两个 scope 都一律 admit
// 且不 panic (防御性: 配置错误的调用方不得让 worker 崩溃)。
func TestStormControllerNilSafeScopes(t *testing.T) {
	var c *StormController
	if r, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); r == nil || outcome != "" || err != nil {
		t.Fatalf("nil controller endpoint: r!=nil=%v outcome=%q err=%v, want admit", r != nil, outcome, err)
	}
	if r, outcome, err := c.AcquireGlobal(context.Background(), 1); r == nil || outcome != "" || err != nil {
		t.Fatalf("nil controller global: r!=nil=%v outcome=%q err=%v, want admit", r != nil, outcome, err)
	}
}
