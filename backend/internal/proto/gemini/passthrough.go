package gemini

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// attachPassthrough 把同一个 Gemini chunk 的 unknown 字段复制到每条
// canonical event，保持 modelFleet / vendorMetadata 等字段可端到端回放。
func attachPassthrough(events []proto.CanonicalEvent, env proto.PassthroughEnvelope) []proto.CanonicalEvent {
	return proto.AttachPassthroughToEvents(events, env)
}
