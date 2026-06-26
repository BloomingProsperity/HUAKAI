// Package router 是 HUAKAI Router Engine —— 跨 pool / 跨 model /
// 跨 cost / 跨 policy 的决策层。
// 参见 docs/specs/_invariants/cross-module-boundaries.md：
//
//   Router Engine    —— 决定尝试哪些路由，以及按什么顺序
//   Resource Pool    —— 决定在一条路由内可以 claim 哪个资源
//   Gateway Executor —— 运行每次 attempt 的循环（claim、forward、settle）
//
// 本包绝不能 import internal/auth（Router 不读取凭证），绝不能持有
// decimal 字段（cost 归属于 Ledger），也绝不能写数据库（Router 不写任何东西）。
//
// 本包是 import-pure 的：调用方的流程为
//   Auth → Registry → Router.Plan(...) → Executor loop → Pool.Claim(...)

package router

import (
	"context"
)

// Router 为一个入站请求生成一个 plan，描述 executor 应当尝试哪条（些）
// 路由。该 plan 仅是数据 —— 不做 IO、不解析凭证、不写数据库。
type Router interface {
	Plan(ctx context.Context, req PlanInput) (RoutePlan, error)
}

// PlanInput 打包 Router 做决策所需的信息：谁在调用、他们请求了哪个
// model、以及他们想完成什么。每一部分来自不同的上游层（Auth /
// Registry / handler）。要瞄准的 pool group 按 Slice 2 N+5b 携带在
// Model.PoolCandidates 内 —— 遗留的 ExplicitPoolGroupID 后门已被移除。
type PlanInput struct {
	Context  RequestContext
	Model    ResolvedModel
	Features RequestFeatures
}

// PlanError 是当无法构建任何 plan 时 Router 返回的类型化错误。
type PlanError struct {
	Code    string // 例如 "no_eligible_pool"、"model_unsupported"、"policy_block"
	Message string
}

func (e *PlanError) Error() string { return e.Code + ": " + e.Message }
