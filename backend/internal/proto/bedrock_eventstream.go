// bedrock_eventstream.go — Bedrock-on-Anthropic 上游事件适配器（A4）。
//
// 上下文：
//   - A2: backend/internal/provider/bedrock/eventstream/decoder.go 解 AWS
//     Binary EventStream 二进制 wire format
//   - A3: backend/internal/gateway/bedrock_stream_scanner.go 把 chunk envelope
//     `{"bytes":"<base64>"}` 解 base64 → emit SSEEvent{Type: 内层 Anthropic
//     event type, Data: 内层 Anthropic 事件 JSON}
//   - A4（本文件）: 把 A3 产出的 SSEEvent.Data（已是纯 Anthropic JSON）转
//     CanonicalEvent
//
// 设计决策（详见 docs/plans/2026-05-08-bedrock-a4-claude.md）：
//   - 独立 struct（不直接复用 AnthropicAdapter）：未来 Bedrock-on-Llama /
//     Bedrock-on-Cohere 不能 delegate Anthropic；matrix 上 Bedrock 的 loss
//     attribution 与 native Anthropic 不同（虽当前等价，但语义不同）
//   - 但内部实现 delegate `*AnthropicAdapter`：Bedrock-on-Anthropic 的 inner
//     payload IS Anthropic 事件 JSON，逐字节复用 AnthropicAdapter 的解析逻辑
//     是 clean-room 友好的 maximally-parsimonious 设计（不是 copy）
//   - 跨包：proto 不能 import gateway（反向依赖）。A5+A6 在 gateway 层用
//     []byte 把 SSEEvent.Data 喂给本 adapter 即可，无需新类型
//
// 当前限定：仅 Bedrock-on-Anthropic（Claude on Bedrock）。OCAW 实测后扩展
// Bedrock-on-Llama / -on-Cohere 时再分流。
package proto

import (
	"context"
	"fmt"
)

// BedrockEventStreamAdapter 把 A3 scanner 产出的 SSEEvent payload（内层
// Anthropic 事件 JSON）翻译为 HCSF CanonicalEvent。
//
// 实现策略：本 adapter 是**完全无状态**的（仅 immutable policy 字段）。
// 每次调用 ProviderEventToCanonicalEvents / FinalizeUpstreamStream 时
// 构造 local AnthropicAdapter delegate — 因为 Bedrock-on-Anthropic 的
// inner JSON 与 native Anthropic 完全同形态。
//
// 为什么不缓存 inner *AnthropicAdapter：registry-shared 的 adapter 实例
// 会被多 goroutine 并发调用（每个请求一个 stream），lazy init `s.inner = ...`
// 会触发 data race。AnthropicAdapter 自身仅持有 immutable bool，per-call
// 构造无开销。（参考 codex lane plan §Decision Points #1 与 §Failure Modes）
//
// 未来扩展点：
//   - Bedrock-on-Llama: 内层 JSON 形态不同，需自实现解析
//   - Bedrock-on-Cohere: 同上
//   - 现在仅 Bedrock-on-Anthropic，故只 delegate
type BedrockEventStreamAdapter struct {
	// CarryForwardSignatureDelta 透传给 per-call 构造的 AnthropicAdapter；
	// 当前 Bedrock 路径默认 false（与 capability matrix 一致）。
	CarryForwardSignatureDelta bool
}

// NewBedrockEventStreamAdapter 构造 Bedrock 上游事件 adapter。
// 当前等价于 &BedrockEventStreamAdapter{}（零值可用），保留构造函数
// 以防未来加内部 init 步骤。
func NewBedrockEventStreamAdapter() *BedrockEventStreamAdapter {
	return &BedrockEventStreamAdapter{}
}

// innerDelegate 返回本次调用专用的 AnthropicAdapter（per-call 构造，
// 无并发 race 风险）。
func (s *BedrockEventStreamAdapter) innerDelegate() *AnthropicAdapter {
	return &AnthropicAdapter{CarryForwardSignatureDelta: s.CarryForwardSignatureDelta}
}

// CanonicalToProviderRequest 占位：A8 才实现 OpenAI→Bedrock-Anthropic
// 请求 body 翻译（R1 决策：本路径优先级 P0，但 A8 才动）。
func (s *BedrockEventStreamAdapter) CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	return nil, nil, ErrNotImplemented
}

// ProviderResponseToCanonical 占位：non-streaming 响应 A8 才动。
func (s *BedrockEventStreamAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	return nil, nil, ErrNotImplemented
}

// ProviderEventToCanonicalEvents 把 Bedrock chunk inner JSON（已被 A3
// scanner base64-decode）转为 CanonicalEvent 列表。
//
// providerEvt 接受形式：
//   - []byte：原始 Anthropic 事件 JSON（A5+A6 集成时 SSEEvent.Data 直传）
//   - anthropicEvent：内部已解析过的形式（测试 / inline 调用）
//
// state 必须是 *UpstreamState（与 AnthropicAdapter 共享，因为 Bedrock-on-
// Anthropic 的 stream 状态机与 native Anthropic 一致）。
//
// **并发安全**：本 adapter 无 mutable 字段，多 goroutine 共享调用安全；
// 但 state（*UpstreamState）必须每流独立——这是接口契约，不是 adapter 责任。
func (s *BedrockEventStreamAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error) {
	if _, ok := state.(*UpstreamState); !ok {
		return nil, nil, fmt.Errorf("proto: BedrockEventStreamAdapter expected *UpstreamState")
	}
	// per-call 构造 — 无 race。AnthropicAdapter 自身 stateless 除 immutable bool。
	return s.innerDelegate().ProviderEventToCanonicalEvents(ctx, providerEvt, state)
}

// FinalizeUpstreamStream 在流终止（EOF / 异常 / cancel）时补齐未结束的
// content_block_stop + message_stop。per-call delegate AnthropicAdapter。
func (s *BedrockEventStreamAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	return s.innerDelegate().FinalizeUpstreamStream(ctx, state)
}

// 编译期接口断言。
var _ UpstreamAdapter = (*BedrockEventStreamAdapter)(nil)

// Source files read:
//   - backend/internal/proto/anthropic_sse.go (HUAKAI 内部，UpstreamState 复用)
//   - backend/internal/proto/proto.go (UpstreamAdapter 接口)
//   - backend/internal/proto/hcsf.go (CanonicalEvent 类型)
//   - backend/internal/gateway/bedrock_stream_scanner.go (A3，emit shape 参考)
//   - https://docs.aws.amazon.com/bedrock/latest/userguide/inference-invoke-stream.html (Bedrock chunk envelope)
// Lane: claude
// Time: 2026-05-08T<UTC>
