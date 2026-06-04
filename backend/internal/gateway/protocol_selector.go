package gateway

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/bedrock"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

// ProtocolAdapterRegistry 按 ProtocolFamily 字符串返回对应的 UpstreamAdapter。
type ProtocolAdapterRegistry interface {
	For(protocolFamily string) (proto.UpstreamAdapter, error)
}

// ErrUnknownProtocolFamily 表示 protocolFamily 没有注册对应的 adapter。
var ErrUnknownProtocolFamily = errors.New("gateway: 未注册该 protocol family 的 upstream adapter")

var errDuplicateProtocolFamily = errors.New("gateway: protocol family 重复注册")

// StaticProtocolAdapterRegistry 是只读静态注册表；启动期 Register 完成后只读。
type StaticProtocolAdapterRegistry struct {
	adapters map[string]proto.UpstreamAdapter
}

// NewStaticProtocolAdapterRegistry 返回空的协议 adapter 注册表。
func NewStaticProtocolAdapterRegistry() *StaticProtocolAdapterRegistry {
	return &StaticProtocolAdapterRegistry{adapters: make(map[string]proto.UpstreamAdapter)}
}

// Register 注册 protocol family 到 upstream adapter 的映射。
func (r *StaticProtocolAdapterRegistry) Register(family string, a proto.UpstreamAdapter) error {
	if r == nil {
		return errors.New("gateway: protocol adapter registry 是 nil")
	}
	if family == "" {
		return errors.New("gateway: protocol family 不能为空")
	}
	if isNilUpstreamAdapter(a) {
		return errors.New("gateway: upstream adapter 不能为 nil")
	}
	if r.adapters == nil {
		r.adapters = make(map[string]proto.UpstreamAdapter)
	}
	if _, ok := r.adapters[family]; ok {
		return fmt.Errorf("%w: %s", errDuplicateProtocolFamily, family)
	}
	r.adapters[family] = a
	return nil
}

// MustRegister 是 Register 的 panic 版本，仅用于启动期确定性注册。
func (r *StaticProtocolAdapterRegistry) MustRegister(family string, a proto.UpstreamAdapter) {
	if err := r.Register(family, a); err != nil {
		panic(err)
	}
}

// For 返回 protocol family 对应的 upstream adapter。
func (r *StaticProtocolAdapterRegistry) For(family string) (proto.UpstreamAdapter, error) {
	if r == nil || r.adapters == nil {
		return nil, fmt.Errorf("%w: registry 未初始化", ErrUnknownProtocolFamily)
	}
	a, ok := r.adapters[family]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProtocolFamily, family)
	}
	return a, nil
}

// BuildDefaultProtocolAdapterRegistry 构造包含当前已实现 adapters 的默认注册表。
//
// 以下 6 家 vendor 使用 openai.Adapter 解析 SSE，因为它们均实现了
// OpenAI Chat Completions 兼容协议（data: {"choices":[...]} 形态）：
//   - deepseek_chat   DeepSeek
//   - mistral_chat    Mistral AI
//   - groqcloud_chat  Groq Cloud
//   - together_chat   Together AI
//   - perplexity_chat Perplexity AI
//   - fireworks_chat  Fireworks AI
func BuildDefaultProtocolAdapterRegistry() *StaticProtocolAdapterRegistry {
	r := NewStaticProtocolAdapterRegistry()
	r.MustRegister("anthropic_messages", &anthropic.Adapter{CarryForwardSignatureDelta: false})
	r.MustRegister("openai_chat", &openai.Adapter{})
	r.MustRegister("openai_responses", &openai.Adapter{})
	// openai_codex 出站到 chatgpt.com/backend-api/codex/completions，
	// 但响应 SSE 形态与 OpenAI Chat Completions 兼容（data: {"choices":[...]}）。
	// 复用 openai.Adapter；若后续观测到形态差异再做专用 session SSE adapter。
	r.MustRegister("openai_codex", &openai.Adapter{})
	r.MustRegister("gemini_messages", &gemini.Adapter{})
	// OpenRouter 是 OpenAI Chat Completions 兼容 meta-aggregator，SSE 形态同 OpenAI。
	r.MustRegister("openrouter_chat", &openai.Adapter{})
	// xAI Grok v1/chat/completions 严格 OpenAI 兼容。
	r.MustRegister("grok_chat", &openai.Adapter{})
	// AWS Bedrock 走二进制 EventStream（非 SSE），由专用 bedrock.EventStreamAdapter
	// （proto/bedrock/eventstream.go，A4 atomic）+ BedrockEventStreamScanner
	// （gateway/bedrock_stream_scanner.go，A3 atomic）成对处理。
	// 当前限定 Bedrock-on-Anthropic（Claude on Bedrock）；future Llama/Cohere
	// on Bedrock 时再 model-family 分流。
	r.MustRegister("bedrock_invoke", bedrock.NewEventStreamAdapter())
	// 以下 6 家均走 OpenAI Chat Completions 兼容 SSE；复用 openai.Adapter。
	r.MustRegister("deepseek_chat", &openai.Adapter{})
	r.MustRegister("mistral_chat", &openai.Adapter{})
	r.MustRegister("groqcloud_chat", &openai.Adapter{})
	r.MustRegister("together_chat", &openai.Adapter{})
	r.MustRegister("perplexity_chat", &openai.Adapter{})
	r.MustRegister("fireworks_chat", &openai.Adapter{})
	// 订阅 session 反转路径。响应 SSE 形态分两类：
	//   - copilot_session:               OpenAI Chat Completions 兼容 → openai.Adapter
	//   - gemini_advanced_session:       Google 内部 SSE 形态，近似 Gemini 官方 → gemini.Adapter
	//   - cursor / antigravity / kiro / windsurf: SSE 形态待 OCAW 采集后确认；
	//     先复用 openai.Adapter 作占位（多数采用 OpenAI Chat 兼容形态），
	//     若实测形态不同再做专用 adapter。
	r.MustRegister("copilot_session", &openai.Adapter{})
	r.MustRegister("gemini_advanced_session", &gemini.Adapter{})
	r.MustRegister("cursor_session", &openai.Adapter{})
	r.MustRegister("antigravity_session", &openai.Adapter{})
	r.MustRegister("kiro_session", &openai.Adapter{})
	r.MustRegister("windsurf_session", &openai.Adapter{})
	return r
}

func isNilUpstreamAdapter(a proto.UpstreamAdapter) bool {
	if a == nil {
		return true
	}
	v := reflect.ValueOf(a)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ ProtocolAdapterRegistry = (*StaticProtocolAdapterRegistry)(nil)
