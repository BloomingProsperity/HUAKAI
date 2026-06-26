// Package adapter 在 F-PROTO-002 的 hub-and-spoke 拓扑下持有各协议 adapter 的实现。
//
// 每个客户端 adapter(openai_chat / openai_responses / anthropic_messages)
// 实现 internal/proto.ClientAdapter。
// 每个上游 adapter(anthropic / openai / gemini / bedrock / antigravity)
// 实现 internal/proto.UpstreamAdapter。
//
// 本包定义 proto hub 的 adapter registry 边界。具体厂商 adapter 在启动时由
// internal/provider/registrydefault(Build())注册，再由 cmd/gateway/wiring.go
// 接入运行中的 gateway;协议族到上游 adapter 的实时映射见该包。
//
// 具体厂商 adapter 位于 internal/provider/<vendor>(anthropic、openai、
// gemini、bedrock……),经 internal/provider/registrydefault.Build() 接线。
package adapter
