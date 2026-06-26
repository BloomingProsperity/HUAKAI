package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// GateFailureRatePrecheck 是当某账号因「再放行一个请求就会越过运营者配置的
// 每分钟请求数(RPM)或 token(TPM)预算」而被从选号中剔除时的失败原因。这是
// 反应式 modelRateLimitGate 的主动式对应物 —— 后者只有在上游已经返回 429 之后
// 才反应,那时请求已经失败了。ROUTE-121。
const GateFailureRatePrecheck GateFailureReason = "rate_precheck_limit"

// RatePrecheckGate 实现 Gate。在以下情况下把某账号从选号中排除:
//   - 该账号有正的 RPMLimit 或 TPMLimit(opt-in),且
//   - 注入了 precheck counter,且
//   - 再放行一个 req.EstimatedInputTokens token 的请求会越过该账号当前的
//     每分钟窗口预算。
//
// 其余所有情况(未配置 limit、counter 为 nil、account 为 nil)都是 no-op ——
// 按设计 fail-open,这样 bug 至多让上限不那么有效,绝不会误把一个健康账号
// 闲置。本 gate 是只读的:预算在 dispatch 时通过 Counter.Record 消费,绝不在
// 这里消费,因此对每个候选账号都运行该 gate 不会多计。
type RatePrecheckGate struct {
	Counter *precheck.Counter // nil → 始终放行(fail-open)
}

// RatePrecheckGateIface 是链路插槽用的具名 gate 接口。
type RatePrecheckGateIface interface{ Gate }

func (g RatePrecheckGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil || g.Counter == nil {
		return true, "", nil
	}
	lim := precheck.Limits{RPM: account.RPMLimit, TPM: account.TPMLimit}
	est := int64(req.EstimatedInputTokens)
	if est < 0 {
		est = 0
	}
	if d := g.Counter.Check(account.ID, lim, est); !d.Allowed {
		return false, GateFailureRatePrecheck, nil
	}
	return true, "", nil
}
