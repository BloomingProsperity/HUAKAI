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
//   - openai_chat              OpenAI Chat Completions 兼容
//   - openai_responses         OpenAI Responses API
//   - openai_codex             OpenAI Codex CLI / ChatGPT Plus session 反转
//   - anthropic_messages       Anthropic Messages
//   - gemini_messages          Google Gemini generativelanguage
//   - openrouter_chat          OpenRouter（OpenAI 兼容）
//   - bedrock_invoke           AWS Bedrock Runtime invoke
//   - grok_chat                xAI Grok（OpenAI 兼容）
//   - deepseek_chat            DeepSeek（OpenAI 兼容）
//   - mistral_chat             Mistral AI（OpenAI 兼容）
//   - groqcloud_chat           Groq Cloud（OpenAI 兼容）
//   - together_chat            Together AI（OpenAI 兼容）
//   - perplexity_chat          Perplexity AI（OpenAI 兼容）
//   - fireworks_chat           Fireworks AI（OpenAI 兼容）
//   - cursor_session           Cursor IDE 网页 session 反转
//   - copilot_session          GitHub Copilot session 反转
//   - gemini_advanced_session  Google Gemini Advanced 网页 session 反转
//   - antigravity_session      Antigravity AI session 反转（占位）
//   - kiro_session             AWS Kiro session 反转（占位）
//   - windsurf_session         Codeium Windsurf session 反转（占位）
package registrydefault

import (
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/deepseek"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/fireworks"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/grok"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/groqcloud"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/kiro"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/mistral"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openrouter"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/perplexity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/together"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
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
	ProtocolDeepSeekChat      = "deepseek_chat"
	ProtocolMistralChat       = "mistral_chat"
	ProtocolGroqCloudChat     = "groqcloud_chat"
	ProtocolTogetherChat      = "together_chat"
	ProtocolPerplexityChat    = "perplexity_chat"
	ProtocolFireworksChat     = "fireworks_chat"
	// 6 家订阅 session 反转路径（OCAW 实施前为 scaffold + TODO header）
	ProtocolCursorSession         = "cursor_session"
	ProtocolCopilotSession        = "copilot_session"
	ProtocolGeminiAdvancedSession = "gemini_advanced_session"
	ProtocolAntigravitySession    = "antigravity_session"
	ProtocolKiroSession           = "kiro_session"
	ProtocolWindsurfSession       = "windsurf_session"
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
	// AutoTranslateAnthropicAPIBody=true 让 Anthropic CLI / Claude Code 直发的
	// Anthropic Messages API body 自动翻译为 Bedrock invoke body
	// (剥离 model + stream, 注入 anthropic_version)。同时联动 Track C
	// 自动 cache_control 注入，长 system prompt 自动命中 vendor 缓存。
	// 这是 Bedrock 端 production 路径的唯一启用方式。
	r.MustRegister(ProtocolBedrockInvoke, &bedrock.PassthroughAdapter{
		AutoTranslateAnthropicAPIBody: true,
	})
	r.MustRegister(ProtocolGrokChat, &grok.PassthroughAdapter{})

	// 以下 6 家为 OpenAI Chat Completions 兼容直通 API key 路径。
	r.MustRegister(ProtocolDeepSeekChat, &deepseek.PassthroughAdapter{})
	r.MustRegister(ProtocolMistralChat, &mistral.PassthroughAdapter{})
	r.MustRegister(ProtocolGroqCloudChat, &groqcloud.PassthroughAdapter{})
	r.MustRegister(ProtocolTogetherChat, &together.PassthroughAdapter{})
	r.MustRegister(ProtocolPerplexityChat, &perplexity.PassthroughAdapter{})
	r.MustRegister(ProtocolFireworksChat, &fireworks.PassthroughAdapter{})

	// 6 家订阅 session 反转路径。每家凭据形态 = SessionToken / UpstreamPassthrough
	// （拒 apikey）；endpoint 部分为占位 TODO，等 OCAW 抓包后替换。
	r.MustRegister(ProtocolCursorSession, &cursor.CursorSessionAdapter{})
	r.MustRegister(ProtocolCopilotSession, &copilot.CopilotSessionAdapter{})
	r.MustRegister(ProtocolGeminiAdvancedSession, &gemini.GeminiAdvancedSessionAdapter{})
	r.MustRegister(ProtocolAntigravitySession, &antigravity.AntigravitySessionAdapter{})
	r.MustRegister(ProtocolKiroSession, &kiro.KiroSessionAdapter{})
	r.MustRegister(ProtocolWindsurfSession, &windsurf.WindsurfSessionAdapter{})

	return r
}
