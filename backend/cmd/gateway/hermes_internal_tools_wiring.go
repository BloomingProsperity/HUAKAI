package main

import (
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// hermes_internal_tools_wiring.go wires the WAVE H3b conversational READ-ONLY
// tool loop: it adapts the EXISTING read-only tool registry into the bridge's
// catalog-provider shape (so the chat payload can carry the tool catalog the LLM
// sees) and builds the internal tool-execute handler the runner calls back into
// mid-conversation. No new tool logic — it reuses the H3 registry + audit store.

// readOnlyCatalogProvider adapts *hermesops.Registry into the bridge's
// hermeschat.ToolCatalogProvider. It surfaces ONLY the read-only catalog (a
// mutating tool is structurally excluded by Registry.ReadOnlyCatalog), marshaled
// into the generic map shape the bridge injects without importing hermesops.
type readOnlyCatalogProvider struct {
	reg *hermesops.Registry
}

// ReadOnlyToolCatalog returns the read-only tools as marshalable maps. A nil
// registry yields nil (no catalog injected) — the chat still works, the LLM just
// has no tools to call.
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
	// KNOB B: thread the runtime tool-loop kill-switch into the handler. When
	// disabled, the handler refuses every runner callback (403) regardless of the
	// bound session. The handler is still constructed (not nil) so a disabled-state
	// call gets a clean 403 rather than a 404 from an unmounted route.
	return hermeschat.NewInternalToolHandler(secret, bindings, reg, calls, nil, toolLoopEnabled, proposer, confirmCache, proposeEnabled)
}
