// Package megroupshttp exposes the authenticated user's visible pool groups
// and their public billing multipliers.
//
// The endpoint merges two concerns a mature account hub usually splits across
// two reads — "which groups can I use" and "what multiplier applies" — into a
// single read-only projection scoped strictly to the session identity. The
// tenant and user are taken only from the validated session; no query or header
// parameter can widen the read (CMB-5). A multiplier is disclosed only when its
// group row carries the operator-controlled public flag; non-public internal
// cost multipliers are never serialized.
package megroupshttp
