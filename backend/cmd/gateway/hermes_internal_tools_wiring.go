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

// hermesToolCatalogProvider 把 *hermesops.Registry 适配成 bridge 的 hermeschat.ToolCatalogProvider,
// 把目录塑形成 bridge 注入用的通用 map(从而 bridge 无需 import hermesops)。注入哪个目录由
// proposeEnabled(Phase B 提议 KNOB)决定:
//   - proposeEnabled=false(默认):注入 ReadOnlyCatalog——只含只读诊断工具,mutating 工具被
//     Registry.ReadOnlyCatalog 结构性排除;注入内容与提议接入前逐字节一致(零行为变)。
//   - proposeEnabled=true:注入 ProposableCatalog——只读工具 PLUS 可提议的 B 级 mutating 工具,
//     每个 mutating 条目带 mutating / requires_confirmation 标志,好让 runner 知道要走 mode=propose
//     并渲染运营者确认;不可提议的 mutating(A 级 / 不可逆)仍被结构性排除。
//
// 该 KNOB 与 internal_tool_handler 的 propose 分支共用同一个开关(HUAKAI_HERMES_LLM_PROPOSE_ENABLED),
// 保证"目录暴露什么"与"handler 接受提议什么"一致。bridge 侧的 toolLoopEnabled(KNOB B)是更上位的门:
// 它关闭时根本不注入任何目录。
type hermesToolCatalogProvider struct {
	reg            *hermesops.Registry
	proposeEnabled bool
}

// ToolCatalog 返回注入给 LLM 的工具目录(已塑形成可 marshal 的 map)。registry 为 nil 时返回 nil
//(不注入目录)——聊天照常工作,LLM 只是没有可调用的工具。proposeEnabled 决定用只读目录还是可提议
// 目录(见类型注释)。
func (p hermesToolCatalogProvider) ToolCatalog() []map[string]any {
	if p.reg == nil {
		return nil
	}
	var tools []hermesops.CatalogTool
	if p.proposeEnabled {
		tools = p.reg.ProposableCatalog()
	} else {
		tools = p.reg.ReadOnlyCatalog()
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		// 只给可提议的 mutating 工具带上标志(只读条目不带这些键 => proposeEnabled=false 时注入内容
		// 与提议接入前逐字节一致)。runner 据此对带 mutating 的工具发 mode=propose 并渲染确认步骤。
		if t.Mutating {
			entry["mutating"] = true
		}
		if t.RequiresConfirmation {
			entry["requires_confirmation"] = true
		}
		out = append(out, entry)
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
