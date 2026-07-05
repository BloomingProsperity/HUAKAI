package proto

import (
	"strings"
	"sync"
)

// P-2 D13.0 — 默认 ClientAdapterRegistry singleton：把已实现的 3 个 client
// adapter（anthropic_messages / openai_chat / openai_responses）注册成默认表，
// 供 gateway / gatewayhttp 在启动期获取。
//
// 设计：
//   - DefaultClientAdapterRegistry 使用 sync.Once 懒初始化；任何调用方在启动
//     期或请求路径取出都安全。
//   - 不暴露 mutable singleton 内部；调用方只读用 Lookup/Protocols。
//   - 测试可用 NewClientAdapterRegistry() 自行构造隔离实例。
//
// 与 D13 完整 forwarder wire-up 的关系：本 slice 只搭表，不动 forwarder /
// handler；下一片 D13.1 在 gatewayhttp 入口注入 RequestMetaSeed 并通过
// registry.Lookup(protocol) 取得对应 adapter。

var (
	defaultClientAdapterRegistryOnce sync.Once
	defaultClientAdapterRegistry     *ClientAdapterRegistry
	defaultClientAdapterFactoriesMu  sync.Mutex
	defaultClientAdapterFactories    []defaultClientAdapterFactory
)

type defaultClientAdapterFactory struct {
	protocol ClientProtocol
	factory  func() ClientAdapter
}

// RegisterDefaultClientAdapterFactory 让各协议子包注册内置的 client adapter，
// 而无需让父级 proto 包反向 import 其子包。必须在 package init 中、在
// DefaultClientAdapterRegistry 首次使用之前调用。
func RegisterDefaultClientAdapterFactory(protocol ClientProtocol, factory func() ClientAdapter) {
	if protocol == "" || factory == nil {
		panic("proto: invalid default client adapter factory")
	}
	defaultClientAdapterFactoriesMu.Lock()
	defer defaultClientAdapterFactoriesMu.Unlock()
	defaultClientAdapterFactories = append(defaultClientAdapterFactories, defaultClientAdapterFactory{
		protocol: protocol,
		factory:  factory,
	})
}

// DefaultClientAdapterRegistry 返回懒初始化的默认 registry；第一次调用时
// 注册 3 个内置 client adapter。后续调用复用同一实例。
//
// 如果 Register 失败（理论上只会因为重复 Register 触发，不会在此处发生），
// 函数 panic 以暴露程序错误（启动期发现，比运行期 silent fail 好）。
func DefaultClientAdapterRegistry() *ClientAdapterRegistry {
	defaultClientAdapterRegistryOnce.Do(func() {
		reg := NewClientAdapterRegistry()
		mustRegister(reg, ClientProtocolAnthropicMessages, &AnthropicMessagesClient{})
		mustRegister(reg, ClientProtocolOpenAIChat, &OpenAIChatClient{})
		mustRegister(reg, ClientProtocolOpenAIResponses, &OpenAIResponsesClient{})
		defaultClientAdapterFactoriesMu.Lock()
		factories := append([]defaultClientAdapterFactory(nil), defaultClientAdapterFactories...)
		defaultClientAdapterFactoriesMu.Unlock()
		for _, f := range factories {
			mustRegister(reg, f.protocol, f.factory())
		}
		defaultClientAdapterRegistry = reg
	})
	return defaultClientAdapterRegistry
}

func mustRegister(reg *ClientAdapterRegistry, protocol ClientProtocol, adapter ClientAdapter) {
	if err := reg.Register(protocol, adapter); err != nil {
		panic("proto: DefaultClientAdapterRegistry register " + string(protocol) + ": " + err.Error())
	}
}

// ClientProtocolByIngressPath 根据 HTTP ingress path 推断 client protocol。
//
// 映射约定（HUAKAI v0.4）：
//   - /v1/chat/completions     → openai_chat
//   - /v1/responses            → openai_responses
//   - /backend-api/codex/responses → openai_responses（Codex CLI 入站）
//   - /v1/messages             → anthropic_messages
//   - /v1/native/openai/responses → openai_responses（native passthrough 原生透传路由）
//   - /v1beta/models...        → gemini
//
// 返回 ok=false 表示路径未识别；调用方应返回 404/400，不要默认 fallback 到
// 任意 adapter（synthesis 反 silent fallback）。
func ClientProtocolByIngressPath(path string) (ClientProtocol, bool) {
	switch path {
	case "/v1/chat/completions":
		return ClientProtocolOpenAIChat, true
	case "/v1/responses", "/v1/native/openai/responses", "/backend-api/codex/responses":
		return ClientProtocolOpenAIResponses, true
	case "/v1/messages":
		return ClientProtocolAnthropicMessages, true
	default:
		if path == "/v1beta/models" || strings.HasPrefix(path, "/v1beta/models/") {
			return ClientProtocolGemini, true
		}
		return "", false
	}
}
