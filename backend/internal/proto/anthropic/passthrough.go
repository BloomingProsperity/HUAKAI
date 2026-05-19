package anthropic

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// attachPassthroughToFirstEvent 把 Anthropic envelope 级 unknown 字段挂到
// 同一上游事件产出的第一条 canonical event，避免客户端重复 emit。
func attachPassthroughToFirstEvent(events []proto.CanonicalEvent, env proto.PassthroughEnvelope) []proto.CanonicalEvent {
	if len(events) == 0 || len(env.Extra) == 0 {
		return events
	}
	events[0].Passthrough = &env
	return events
}
