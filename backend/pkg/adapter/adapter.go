// Package adapter holds protocol adapter implementations under the
// F-PROTO-002 hub-and-spoke topology.
//
// Each client adapter (openai_chat / openai_responses / anthropic_messages)
// implements internal/proto.ClientAdapter.
// Each upstream adapter (anthropic / openai / gemini / bedrock / antigravity)
// implements internal/proto.UpstreamAdapter.
//
// Current slice keeps this package as the adapter registry boundary. Concrete
// registration across OpenAI/Gemini/Bedrock/Antigravity remains Phase E+ work.
//
// Each concrete adapter lives in a sub-package, e.g. pkg/adapter/openaichat,
// pkg/adapter/anthropicupstream, etc. Implementation in Phase 4 vertical slices.
package adapter

// TODO(phase-4): import + register adapter map per client_protocol /
// upstream_protocol in HUAKAI's adapter registry (per F-PROTO-002 §9 +
// Codex Portkey cross-verify P1).
