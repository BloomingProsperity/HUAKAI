package openai

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// attachPassthrough 把同一个 OpenAI chunk 的 unknown 字段复制到每条
// canonical event，避免多事件 chunk 只有首事件可回放 vendor 扩展字段。
func attachPassthrough(events []proto.CanonicalEvent, env proto.PassthroughEnvelope) []proto.CanonicalEvent {
	return proto.AttachPassthroughToEvents(events, env)
}
