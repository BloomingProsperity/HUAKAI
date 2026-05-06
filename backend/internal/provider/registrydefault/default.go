// 包 registrydefault — 启动期注入"全部已实现 vendor adapter"到一个
// provider.StaticRegistry。main 与 admin 启动时调 Build()。
//
// 边界（Owner 2026-05-06 directive）：
//   仅注册"已实现"的 adapter。Anthropic OAuth 反转、ChatGPT 反转、
//   Cursor / Copilot / Kiro / Windsurf / Antigravity 反转等尚未实现的
//   protocol family 不在此处注册；运行期访问会得到 provider.
//   ErrAdapterNotRegistered，由配置层 reject 阻止误用。
//
// Protocol family 字符串约定（与 router.ResolvedModel.ProtocolFamily 对齐）：
//   - openai_chat            OpenAI Chat Completions 兼容
//   - openai_responses       OpenAI Responses API
//   - openai_codex           OpenAI Codex CLI / ChatGPT Plus session 反转
//   - anthropic_messages     Anthropic Messages
//   - gemini_messages        Google Gemini generativelanguage
//   - openrouter_chat        OpenRouter（OpenAI 兼容）
//   - bedrock_invoke         AWS Bedrock Runtime invoke
//   - grok_chat              xAI Grok（OpenAI 兼容）
package registrydefault

import (
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/grok"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openrouter"
)

// Protocol family 常量。供配置层与 router 共享。
const (
	ProtocolOpenAIChat        = "openai_chat"
	ProtocolOpenAIResponses   = "openai_responses"
	ProtocolOpenAICodex       = "openai_codex"
	ProtocolAnthropicMessages = "anthropic_messages"
	ProtocolGeminiMessages    = "gemini_messages"
	ProtocolOpenRouterChat    = "openrouter_chat"
	ProtocolBedrockInvoke     = "bedrock_invoke"
	ProtocolGrokChat          = "grok_chat"
)

// Build 创建注册表并注册全部已实现 vendor adapter。失败时 panic — 启动
// 期问题不应静默吞，必须 fail-loud。
func Build() *provider.StaticRegistry {
	r := provider.NewStaticRegistry()

	// OpenAI Chat Completions（v1/chat/completions）
	r.MustRegister(ProtocolOpenAIChat, &openai.PassthroughAdapter{})

	// 当前阶段 OpenAI Responses API 也走同一 PassthroughAdapter（路径
	// 同为 v1/chat/completions 的 caller 行为；Responses API 的专属
	// adapter 待后续 atomic 单独实现）。注册到独立 protocol family，
	// 后续切换 adapter 不影响 router 配置。
	r.MustRegister(ProtocolOpenAIResponses, &openai.PassthroughAdapter{})

	// OpenAI Codex CLI / ChatGPT Plus session 反转。出站到 chatgpt.com
	// 自有 backend，凭据形态为 session_token / upstream_passthrough（拒
	// apikey）。Endpoint 默认 chatgpt.com/backend-api/codex/completions，
	// 可在 adapter 字段覆盖。
	r.MustRegister(ProtocolOpenAICodex, &openai.CodexSessionAdapter{})

	r.MustRegister(ProtocolAnthropicMessages, &anthropic.PassthroughAdapter{})
	r.MustRegister(ProtocolGeminiMessages, &gemini.PassthroughAdapter{})
	r.MustRegister(ProtocolOpenRouterChat, &openrouter.PassthroughAdapter{})
	r.MustRegister(ProtocolBedrockInvoke, &bedrock.PassthroughAdapter{})
	r.MustRegister(ProtocolGrokChat, &grok.PassthroughAdapter{})

	return r
}
