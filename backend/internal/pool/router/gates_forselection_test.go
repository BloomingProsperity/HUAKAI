package router

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingPreparerGate 实现 Gate + SelectionGatePreparer, 分别统计:
//   - prepareCalls: PrepareForSelection 被调次数(应每 Select 恰 1 次)
//   - originalAllow: 原始 gate 的 Allow 被调次数(应为 0 — 已被 prepared 副本取代)
//   - preparedAllow: prepared gate 的 Allow 被调次数(应 >=1 — 真正参与逐候选过滤)
type countingPreparerGate struct {
	prepareCalls  *int32
	originalAllow *int32
	preparedAllow *int32
}

func (g countingPreparerGate) Allow(context.Context, *AccountSnapshot, SelectionRequest) (bool, GateFailureReason, error) {
	atomic.AddInt32(g.originalAllow, 1)
	return true, "", nil
}

func (g countingPreparerGate) PrepareForSelection(context.Context, SelectionRequest) Gate {
	atomic.AddInt32(g.prepareCalls, 1)
	return preparedCountingGate{preparedAllow: g.preparedAllow}
}

type preparedCountingGate struct{ preparedAllow *int32 }

func (g preparedCountingGate) Allow(context.Context, *AccountSnapshot, SelectionRequest) (bool, GateFailureReason, error) {
	atomic.AddInt32(g.preparedAllow, 1)
	return true, "", nil
}

// TestForSelection_PreparesGroupPolicyGateOncePerSelect 守 ForSelection 的核心不变量:
// 实现 SelectionGatePreparer 的 gate 在一次 Select 内只 prepare 一次(查库一次), 其
// 返回的 prepared gate 才是逐候选过滤实际使用的 gate。这是把 routes 查询从每候选
// hoist 到每 Select 的判别测。
//
// 判别(mutation):
//   - Select 改回逐候选直接用 s.gates(不调 ForSelection) → prepareCalls==0 红, 且
//     originalAllow>0(原始 gate 被直接逐候选调用)。
//   - prepare 误置于 per-candidate 循环 → prepareCalls==候选数(3) 红。
//   - ForSelection 未把 prepared gate 装回链(返回原 gate) → preparedAllow==0 红。
func TestForSelection_PreparesGroupPolicyGateOncePerSelect(t *testing.T) {
	var prepareCalls, originalAllow, preparedAllow int32
	gate := countingPreparerGate{
		prepareCalls:  &prepareCalls,
		originalAllow: &originalAllow,
		preparedAllow: &preparedAllow,
	}
	chain := DefaultGateChain()
	chain.GroupPolicy = gate

	accounts := []*AccountSnapshot{
		snap(1, 100, 0, 0, time.Time{}),
		snap(2, 100, 0, 0, time.Time{}),
		snap(3, 100, 0, 0, time.Time{}),
	}
	sel := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithGateChain(chain),
		WithSlotManager(newMemSlotManager()),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 100, RequestedModel: "claude-3-5-sonnet"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AccountID == 0 {
		t.Fatalf("expected a selected account, got %+v", res)
	}

	if got := atomic.LoadInt32(&prepareCalls); got != 1 {
		t.Fatalf("PrepareForSelection called %d times, want exactly 1 per Select (hoist invariant)", got)
	}
	if got := atomic.LoadInt32(&preparedAllow); got < 1 {
		t.Fatalf("prepared gate Allow called %d times, want >=1 (prepared gate must drive per-candidate filtering)", got)
	}
	if got := atomic.LoadInt32(&originalAllow); got != 0 {
		t.Fatalf("original gate Allow called %d times, want 0 (original must be replaced by prepared copy)", got)
	}

	// 守"scoped 链绝不写回 s.gates"的并发/跨请求不变量(codex review A S2):
	// ForSelection 是值接收返回局部副本, 一次 Select 的准备绝不能污染 selector 共享的
	// s.gates。否则并发 Select 会互相串改、跨请求复用陈旧 prepared gate。
	// 1) 一次 Select 后, s.gates.GroupPolicy 必须仍是原始 preparer 而非 prepared 副本。
	//    mutation: ForSelection 改成写回 receiver / Select 改成 s.gates=s.gates.ForSelection(...)
	//    → 此处类型断言失败变红。
	if _, ok := sel.gates.GroupPolicy.(countingPreparerGate); !ok {
		t.Fatalf("after Select, s.gates.GroupPolicy is %T, want original countingPreparerGate (ForSelection must not write back to receiver)", sel.gates.GroupPolicy)
	}
	// 2) 第二次 Select 必须再 prepare 一次(每 Select 独立准备)。若 ForSelection 写回了
	//    receiver, 第二次会对已 prepared(不实现 preparer)的 gate 调用 → prepareCalls 停在 1 → 红。
	if _, err := sel.Select(context.Background(), SelectionRequest{TenantID: 100, RequestedModel: "claude-3-5-sonnet"}); err != nil {
		t.Fatalf("second Select: %v", err)
	}
	if got := atomic.LoadInt32(&prepareCalls); got != 2 {
		t.Fatalf("after 2 Selects, PrepareForSelection called %d times, want 2 (each Select prepares fresh; write-back to receiver would stick at 1)", got)
	}
}

// TestForSelection_NonPreparerGateUnchanged 守行为保持: 不实现 preparer 的 gate
// (如 AllowAllGate)经 ForSelection 后语义不变 —— 接线前 ForSelection 是恒等变换。
// 判别: 若 ForSelection 错误地吞掉/改写非 preparer gate, AllowAll 的恒放行会被破坏,
// Select 将选不到账号 → 红。
func TestForSelection_NonPreparerGateUnchanged(t *testing.T) {
	chain := DefaultGateChain() // GroupPolicy = AllowAllGate(不实现 preparer)
	scoped := chain.ForSelection(context.Background(), SelectionRequest{TenantID: 7})
	ok, _, err := scoped.GroupPolicy.Allow(context.Background(), snap(1, 7, 0, 0, time.Time{}), SelectionRequest{TenantID: 7})
	if err != nil || !ok {
		t.Fatalf("non-preparer GroupPolicy gate after ForSelection: ok=%v err=%v, want allow (identity transform)", ok, err)
	}
}
