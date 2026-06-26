package hermesops

// catalog.go builds the sanitized tool catalogs the conversational ops assistant
// (the LLM in the Python runner) sees. Two catalogs exist:
//   - ReadOnlyCatalog: ONLY read-only diagnostic tools (the current/default
//     conversational surface). A mutating tool can never appear here, so the LLM
//     cannot name one it was never told exists.
//   - ProposableCatalog: read-only tools PLUS mutating (B-level) tools, each
//     mutating entry explicitly flagged. It lets the LLM PROPOSE a mutation —
//     never execute it: the propose path runs only the tool's read-only Resolve
//     (dry-run preview + single-use correlation_id); the mutation runs only after
//     the OPERATOR confirms via a separate operator-authenticated path. Gated
//     behind a default-OFF KNOB at the wiring; selecting it is an Owner decision.

// CatalogTool is one entry in the LLM-facing tool catalog: the identity +
// description + input-schema hints needed for the model to choose a tool and
// shape its arguments. It carries NO Run/Resolve/Mutate function — only the
// descriptive surface.
type CatalogTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]string `json:"input_schema"`
	// Mutating marks a tool the LLM may PROPOSE but never execute. It is set ONLY
	// by ProposableCatalog; ReadOnlyCatalog never sets it. With omitempty a false
	// value is absent from the wire, so ReadOnlyCatalog's JSON is byte-unchanged by
	// these fields existing. A proposed mutating tool goes through dry-run preview +
	// operator confirmation; the LLM-propose path can never run its Mutate.
	Mutating bool `json:"mutating,omitempty"`
	// RequiresConfirmation tells the runner/LLM to render a confirmation step
	// (operator must approve) before the mutation commits. Set together with Mutating.
	RequiresConfirmation bool `json:"requires_confirmation,omitempty"`
}

// ReadOnlyCatalog returns the catalog of READ-ONLY diagnostic tools from the
// registry, sorted by name. A mutating tool is filtered out here regardless of
// its other flags — the gate is spec.Mutating, the same flag the dispatch path
// (Run) uses to refuse a mutation. The result is safe to inject into the LLM's
// context: it exposes only tool identity + read-only arg hints, never a
// function, a credential, or a mutating capability.
//
// SAFETY: this is the FIRST of two independent read-only gates on the
// conversational path. Even if the LLM were somehow told a mutating tool name
// out-of-band, the internal endpoint's own filter (and Registry.Run's
// ErrNotMutating) still refuse it. Dropping THIS filter alone does not expose a
// mutation; dropping the endpoint filter alone does not either — both must fail.
func (r *Registry) ReadOnlyCatalog() []CatalogTool {
	if r == nil {
		return nil
	}
	out := make([]CatalogTool, 0, len(r.tools))
	for _, s := range r.List() { // List() is already name-sorted + stable.
		if s.Mutating || !s.ReadOnly {
			// Structural exclusion: a mutating (or non-read-only) tool is NEVER
			// described to the LLM. This is the catalog-side half of the
			// read-only guarantee.
			continue
		}
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		out = append(out, CatalogTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: copyStringMap(schema),
		})
	}
	return out
}

// ProposableCatalog returns the catalog of tools the LLM may PROPOSE in the
// conversational ops path: all read-only diagnostic tools PLUS the mutating
// (B-level) tools, with each mutating entry explicitly flagged Mutating +
// RequiresConfirmation. Sorted by name.
//
// Unlike ReadOnlyCatalog, this DOES include mutating tools — but ONLY so the LLM
// can PROPOSE one. It can never EXECUTE one: the LLM-propose path
// (internal_tool_handler propose branch, landing in a later PR) runs only the
// tool's read-only Resolve to produce a dry-run preview + a single-use
// correlation_id; the actual mutation runs only after the OPERATOR confirms via a
// separate operator-authenticated path. The flags let the runner/LLM render
// "this is a change — it needs your confirmation".
//
// SAFETY: exposing a mutating tool's NAME + arg hints to the LLM is not itself a
// mutation capability. The four structural fail-closed gates (propose path has no
// Mutate method; confirm is operator-only-authenticated; correlation_id is
// single-use + six-tuple-bound; KNOBs default off) live downstream. This method
// has NO caller until the propose wiring lands (deliberately dead until then) and
// is gated behind a default-OFF KNOB at the wiring — merging it changes no
// production behavior, and ReadOnlyCatalog is left byte-for-byte unchanged.
func (r *Registry) ProposableCatalog() []CatalogTool {
	if r == nil {
		return nil
	}
	out := make([]CatalogTool, 0, len(r.tools))
	for _, s := range r.List() { // List() is already name-sorted + stable.
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		entry := CatalogTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: copyStringMap(schema),
		}
		switch {
		case s.Mutating:
			// A mutating tool is proposable but flagged: the LLM may name it, the
			// runner renders a confirmation step, and execution is gated on the
			// operator. (A tool flagged BOTH mutating + read-only is treated as
			// mutating here — fail safe toward "needs confirmation".)
			entry.Mutating = true
			entry.RequiresConfirmation = true
		case s.ReadOnly:
			// read-only diagnostic: proposable with no flags, runs directly.
		default:
			// Neither read-only nor mutating: an unclassified tool is excluded
			// (defensive — every real tool is classified).
			continue
		}
		out = append(out, entry)
	}
	return out
}

// copyStringMap returns a shallow copy so a caller cannot mutate the registry's
// InputSchema map through the returned catalog entry.
func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
