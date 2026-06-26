package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
)

// GateFailureWindowCost 是当某账号因其 5 小时会话窗口的花费达到运营者
// 配置的上限而被移出轮换时的失败原因。
const GateFailureWindowCost GateFailureReason = "window_cost_limit"

// WindowCostGate 实现 Gate。当满足以下全部条件时，它将账号排除在选择之外：
//   - 该账号的 WindowCostLimitCents 为正，且
//   - cache 中存在该账号的新鲜条目，且
//   - 缓存的花费 >= 上限。
//
// 其余所有情况（limit==0、cache miss、过期条目、nil reader）该 gate 都是
// 空操作 —— 设计上 fail-open，使得 bug 至多让上限失效，绝不会错误地把
// 健康账号挤下场。
type WindowCostGate struct {
	Reader windowcost.CostReader // nil → 始终放行（fail-open）
}

// WindowCostGateInterface 是链路槽位上的具名 gate 接口。
type WindowCostGateInterface interface{ Gate }

func (g WindowCostGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return true, "", nil
	}
	limit := account.WindowCostLimitCents
	if limit <= 0 {
		// 按需开启：未设置上限 → 始终合格
		return true, "", nil
	}
	if g.Reader == nil {
		// 未注入 cache → fail-open
		return true, "", nil
	}
	cents, fresh := g.Reader.CurrentCost(account.ID)
	if !fresh {
		// cache miss 或已过期 → fail-open
		return true, "", nil
	}
	if cents >= limit {
		return false, GateFailureWindowCost, nil
	}
	return true, "", nil
}
