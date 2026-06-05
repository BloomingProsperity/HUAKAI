// Package adapter holds protocol adapter implementations under the
// F-PROTO-002 hub-and-spoke topology.
//
// Each client adapter (openai_chat / openai_responses / anthropic_messages)
// implements internal/proto.ClientAdapter.
// Each upstream adapter (anthropic / openai / gemini / bedrock / antigravity)
// implements internal/proto.UpstreamAdapter.
//
// This package defines the adapter registry boundary for the proto hub.
// Concrete vendor adapters are registered at startup by
// internal/provider/registrydefault (Build()), which cmd/gateway/wiring.go
// wires into the running gateway; see that package for the live mapping of
// protocol family to upstream adapter.
//
// Concrete vendor adapters live under internal/provider/<vendor> (anthropic, openai,
// gemini, bedrock, ...), wired via internal/provider/registrydefault.Build().
package adapter
