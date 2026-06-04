// Package proto implements F-PROTO-002: protocol translation across provider
// pairs (OpenAI Chat / Responses / Anthropic Messages × upstream protocols)
// via HUAKAI Canonical Stream Format (HCSF).
//
// See docs/specs/protocol-translation.md for the released spec.
// The package has shared protocol contracts plus an Anthropic streaming
// adapter path used by the gateway smoke flow.
package proto

import "context"

// HCSF v0.4 实化为 HCSFEnvelope 的临时 alias；P-2 ClientAdapter 落地时删除 alias，
// 调用方改用 HCSFEnvelope 直接命名。Day 8 sunset 标记保留于此。
//
// 兼容性：现有 ClientAdapter / UpstreamAdapter 接口签名 *HCSF 等价 *HCSFEnvelope。
// P-0c-C 已修复历史 `&HCSF{}` 零值穿透问题（proto/openai/sse.go /
// proto/gemini/sse.go），现统一返回至少 Version + BufferedResponse 的最小 envelope；adapter 边界处用
// ValidateEnvelopeVersionGuard 做轻量守门，debug build 用 ValidateEnvelopeDebug
// 触发完整 校验。
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
