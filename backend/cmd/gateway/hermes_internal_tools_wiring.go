package main

import (
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// hermes_internal_tools_wiring.go 接线 WAVE H3b 的对话式「只读」工具回路:它把「现有」的
// 只读工具 registry 适配成 bridge 的 catalog-provider 形态(使 chat payload 能携带 LLM
// 所看到的工具目录),并构造 runner 在对话中途回调的内部 tool-execute handler。无任何新工具
// 逻辑 —— 它复用 H3 的 registry + 审计 store。

// readOnlyCatalogProvider 把 *hermesops.Registry 适配成 bridge 的
// hermeschat.ToolCatalogProvider。它「仅」暴露只读目录(mutating 工具被
// Registry.ReadOnlyCatalog 在结构上排除),并 marshal 成 bridge 注入用的通用 map 形态,
// 无需导入 hermesops。
type readOnlyCatalogProvider struct {
	reg *hermesops.Registry
}

// ReadOnlyToolCatalog 把只读工具返回为可 marshal 的 map。registry 为 nil 时返回 nil
//(不注入目录)—— chat 仍能工作,只是 LLM 没有可调用的工具。
func (p readOnlyCatalogProvider) ReadOnlyToolCatalog() []map[string]any {
	if p.reg == nil {
		return nil
	}
	tools := p.reg.ReadOnlyCatalog()
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return out
}

// buildHermesInternalToolHandler 构造面向 runner 的 tool-execute handler。它用与 bridge 相同的
// internal-token secret(以校验会话的 internal_token)、共享的会话 bindings(以解析 operator)、
// 只读 registry(Run 拒绝 mutation)、以及 hermes_tool_calls inserter(审计)接线。registry /
// inserter / bindings 为 nil 时,handler 在请求期 fail-closed。
//
// 末尾两个参数接入 Phase B LLM-提议路径:confirmCache 是共享的 correlation-id store(与 operator
// H1 确认路径读取的是同一个实例,故 operator 确认的正是 LLM 所提的那条提议),proposeEnabled 是
// 提议 KNOB(默认关 => 激活前提议分支惰性 / 403)。同一个 registry 兼作 ProposalResolver(它同时
// 满足两个接口);它的 ResolveProposal 只读、不持有任何 Mutate 句柄。
func buildHermesInternalToolHandler(secret []byte, bindings *hermeschat.SessionBindings, reg *hermesops.Registry, inserter *hermestoolsdb.Queries, toolLoopEnabled bool, confirmCache *hermesconfirm.Cache, proposeEnabled bool) *hermeschat.InternalToolHandler {
	if reg == nil || bindings == nil || len(secret) == 0 {
		return nil
	}
	var calls hermesops.ToolCallInserter
	if inserter != nil {
		calls = inserter
	}
	// reg 经 Registry.ResolveProposal 满足 hermeschat.ProposalResolver(只读 dry-run、不暴露任何
	// Mutate 句柄)。confirmCache 为 nil 或 proposeEnabled=false 时,提议分支保持 fail-closed
	//(503 / 403)——零行为变。
	var proposer hermeschat.ProposalResolver = reg
	// KNOB B:把运行期工具回路的 kill-switch 接入 handler。禁用时,无论会话绑定如何,
	// handler 都拒绝每一次 runner 回调(403)。handler 仍会被构造(非 nil),使禁用态下的
	// 调用拿到一个干净的 403,而非未挂载路由的 404。
	return hermeschat.NewInternalToolHandler(secret, bindings, reg, calls, nil, toolLoopEnabled, proposer, confirmCache, proposeEnabled)
}
