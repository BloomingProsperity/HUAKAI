package main

import (
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
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

// buildHermesInternalToolHandler constructs the runner-facing READ-ONLY
// tool-execute handler. It is wired with the SAME internal-token secret as the
// bridge (so it verifies the session's internal_token), the shared session
// bindings (so it resolves the operator), the read-only registry (Run refuses a
// mutation), and the hermes_tool_calls inserter (audit). A nil registry / nil
// inserter / nil bindings makes the handler fail closed at request time.
func buildHermesInternalToolHandler(secret []byte, bindings *hermeschat.SessionBindings, reg *hermesops.Registry, inserter *hermestoolsdb.Queries) *hermeschat.InternalToolHandler {
	if reg == nil || bindings == nil || len(secret) == 0 {
		return nil
	}
	var calls hermesops.ToolCallInserter
	if inserter != nil {
		calls = inserter
	}
	return hermeschat.NewInternalToolHandler(secret, bindings, reg, calls, nil)
}
