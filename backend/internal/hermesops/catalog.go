package hermesops

// catalog.go builds the sanitized, READ-ONLY tool catalog the conversational
// ops assistant (the LLM in the Python runner) sees. It is the single place that
// decides WHICH tools the LLM may even know about, and it filters to read-only
// tools STRUCTURALLY: a mutating tool can never appear in the catalog, so the
// LLM cannot name one it was never told exists, and — combined with the
// internal endpoint's own read-only filter (defense in depth) — a mutation is
// unreachable from this conversational path.

// CatalogTool is one entry in the LLM-facing tool catalog: the identity +
// description + input-schema hints needed for the model to choose a tool and
// shape its arguments. It carries NO Run/Resolve/Mutate function and NO mutating
// metadata — only the read-only descriptive surface.
type CatalogTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]string `json:"input_schema"`
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

// copyStringMap returns a shallow copy so a caller cannot mutate the registry's
// InputSchema map through the returned catalog entry.
func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
