package router

import (
	"context"
)

// GateFailureContextWindow 是当某候选项因「请求的估算 input token 加上
// 预留的 output 余量超过所请求 model 的有效上下文窗口」而被从选择中剔除时
// 记录的失败原因。
const GateFailureContextWindow GateFailureReason = "context_window"

// ContextWindowGate 实现 Gate。它是一个分发前的准入预检:
// 将估算的 prompt 大小(input token 加上客户端预留的任何 output 余量)
// 与 SelectionRequest 上携带的 per-MODEL 上下文窗口比较,当请求放不下时
// 剔除该候选项。
//
// 在 HUAKAI 中上下文窗口是 per-model 属性而非 per-account,所以
//(不同于 WindowCostGate / SessionCountGate)本 gate 只读请求字段,
// 完全忽略 AccountSnapshot —— 同一请求的每个候选项共享相同窗口,
// 因此一个溢出则全部溢出,这正是应该回退到 model fallback 的信号。
//
// 它从不硬拒绝。当所有候选项都溢出时,Select 返回
// ErrNoEligibleAccount,分发层将其映射为「无容量」并路由进既有的
// model-fallback 循环 —— 优雅降级,而非 4xx。
//
// 默认 fail-open:
//   - ModelContextWindow <= 0(窗口未知 / 未配置)→ 始终放行;
//   - EstimatedInputTokens <= 0(未接入估算)→ 始终放行。
//
// 只有当两者都为正,且 EstimatedInputTokens + reservedOutput 严格超过
// 窗口时,候选项才会被剔除。比较是严格的(>),因此恰好放得下的请求
// 会被放行。
type ContextWindowGate struct{}

// ContextWindowGateIface 是用于 chain 槽位的具名 gate 接口。
type ContextWindowGateIface interface{ Gate }

func (ContextWindowGate) Allow(_ context.Context, _ *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	window := req.ModelContextWindow
	if window <= 0 {
		// per-model 窗口未知 / 未配置 → fail-open(不剔除)。
		return true, "", nil
	}
	estimate := req.EstimatedInputTokens
	if estimate <= 0 {
		// 该请求未接入 prompt 估算 → fail-open。
		return true, "", nil
	}
	reservedOutput := req.MaxOutputTokens
	if reservedOutput < 0 {
		reservedOutput = 0
	}
	if estimate+reservedOutput > window {
		return false, GateFailureContextWindow, nil
	}
	return true, "", nil
}
