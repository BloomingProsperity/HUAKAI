// Package proto implements F-PROTO-002: protocol translation across provider
// pairs (OpenAI Chat / Responses / Anthropic Messages × upstream protocols)
// via HUAKAI Canonical Stream Format (HCSF).
//
// See docs/specs/protocol-translation.md for the released spec.
// Phase 3 skeleton ONLY per DR-008.
package proto

import "context"

// HCSF is the HUAKAI Canonical Stream Format — the intermediate type
// across all client × upstream pairs.
type HCSF struct {
	// TODO(phase-4): canonical request + response + event types.
	// Aligned with OpenAI Responses semantics but in HUAKAI's own type names.
}

// ClientAdapter handles client-protocol ↔ canonical translation.
type ClientAdapter interface {
	RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error)
	CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error)
	CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error)
	FinalizeClientStream(ctx context.Context, state any) ([][]byte, error)
}

// UpstreamAdapter handles canonical ↔ upstream-protocol translation.
type UpstreamAdapter interface {
	CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error)
	ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error)
	ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error)
	FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error)
}

// ProtocolLossEntry per spec §4.2.
type ProtocolLossEntry struct {
	Feature   string  `json:"feature"`
	Direction string  `json:"direction"`
	Verdict   Verdict `json:"verdict"`
	Note      string  `json:"note"`
}

// Verdict per spec §4.0 capability decision criteria.
type Verdict string

const (
	VerdictPreserved   Verdict = "PRESERVED"
	VerdictLossy       Verdict = "LOSSY"
	VerdictUnsupported Verdict = "UNSUPPORTED"
)

// Direction per spec §4.2.
type Direction string

const (
	DirectionClientToCanonical   Direction = "client_to_canonical"
	DirectionCanonicalToUpstream Direction = "canonical_to_upstream"
	DirectionUpstreamToCanonical Direction = "upstream_to_canonical"
	DirectionCanonicalToClient   Direction = "canonical_to_client"
)

// TODO(phase-4): implement adapters for openai_chat / openai_responses /
// anthropic_messages clients × anthropic / openai / gemini / bedrock /
// antigravity upstreams. Capability matrix backing in protocol_capability_matrix
// table per docs/schema/protocol-translation.sql.
