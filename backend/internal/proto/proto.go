// Package proto implements F-PROTO-002: protocol translation across provider
// pairs (OpenAI Chat / Responses / Anthropic Messages × upstream protocols)
// via HUAKAI Canonical Stream Format (HCSF).
//
// See docs/specs/protocol-translation.md for the released spec.
// Current slice has the shared protocol contracts plus an Anthropic streaming
// adapter path used by the gateway smoke flow. Full HCSF and additional
// provider/client adapters remain Phase E+ work.
package proto

import "context"

// HCSF v0.4 实化为 HCSFEnvelope 的临时 alias；P-2 ClientAdapter 落地时删除 alias，
// 调用方改用 HCSFEnvelope 直接命名。Day 8 sunset 标记保留于此。
//
// 兼容性：现有 ClientAdapter / UpstreamAdapter 接口签名 *HCSF 等价 *HCSFEnvelope；
// 现有 `&HCSF{}` 调用点（openai_sse.go / gemini_sse.go 等）将构造零值 envelope，
// 通过 ValidateEnvelope 时会因 Version 非 "0.4" 而失败 —— 这是计划中的迁移压力。
type HCSF = HCSFEnvelope

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

// ProtocolLossEntry v0.3 stub 已迁移至 protocol_loss.go（v0.4 升级，含 v0.3 兼容字段）。

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
