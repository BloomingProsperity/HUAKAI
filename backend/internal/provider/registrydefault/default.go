// 包 registrydefault — 启动期注入"全部已实现 vendor adapter"到一个
// provider.StaticRegistry。main 与 admin 启动时调 Build()。
//
// 边界（Owner 2026-05-06 directive）：
//
//	仅默认注册已验证 adapter。未验证的 session placeholder adapter 仅在
//	HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=true 时 opt-in 注册。运行期
//	访问未注册 family 会得到 provider.ErrAdapterNotRegistered，由配置层
//	reject 阻止误用。
//
// Protocol family 字符串约定（与 router.ResolvedModel.ProtocolFamily 对齐）：
//   - openai_chat              OpenAI Chat Completions 兼容
//   - openai_responses         OpenAI Responses API
//   - openai_codex             OpenAI Codex CLI / ChatGPT Plus session 反转
//   - anthropic_messages       Anthropic Messages
//   - anthropic_claude_session Anthropic Pro/Max OAuth session 反转
//   - gemini_messages          Google Gemini generativelanguage
//   - openrouter_chat          OpenRouter（OpenAI 兼容）
//   - bedrock_invoke           AWS Bedrock Runtime invoke
//   - grok_chat                xAI Grok（OpenAI 兼容）
//   - kimi_chat                Kimi / Moonshot（OpenAI 兼容）
//   - deepseek_chat            DeepSeek（OpenAI 兼容）
//   - mistral_chat             Mistral AI（OpenAI 兼容）
//   - groqcloud_chat           Groq Cloud（OpenAI 兼容）
//   - together_chat            Together AI（OpenAI 兼容）
//   - perplexity_chat          Perplexity AI（OpenAI 兼容）
//   - fireworks_chat           Fireworks AI（OpenAI 兼容）
//   - qwen_chat                通义千问 Qwen / 阿里 DashScope（OpenAI 兼容）
//   - glm_chat                 智谱 GLM/ChatGLM / BigModel（OpenAI 兼容）
//   - yi_chat                  零一万物 Yi / 01.AI（OpenAI 兼容）
//   - baichuan_chat            百川大模型（OpenAI 兼容）
//   - cohere_chat              Cohere（OpenAI 兼容 /compatibility/v1）
//   - ollama_native            Ollama 原生 /api/chat（NDJSON 流式；与 ollama_chat 并存）
//   - dify_chat                Dify 应用 API（per-app token；bot_type 分端点）
//   - replicate_image          Replicate 图片生成（models/{model}/predictions；图片 lane 专用）
//   - vertex_gemini            Gemini-on-Vertex（publishers/google；generateContent/streamGenerateContent）
//   - vertex_anthropic         Anthropic-on-Vertex（publishers/anthropic；rawPredict/streamRawPredict + body reshape）
//   - cursor_session           Cursor IDE 网页 session 反转
//   - copilot_session          GitHub Copilot session 反转
//   - gemini_advanced_session  Google Gemini Advanced 网页 session 反转
//   - antigravity_session      Antigravity AI session 反转（占位）
//   - kiro_session             AWS Kiro session 反转（占位）
//   - windsurf_session         Codeium Windsurf session 反转（占位）
package registrydefault

import (
	"os"

	_ "github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
	providerdify "github.com/BloomingProsperity/HUAKAI/internal/provider/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/kiro"
	providerollama "github.com/BloomingProsperity/HUAKAI/internal/provider/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openrouter"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/replicate"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/vertex"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
)

// Protocol family 常量。供配置层与 router 共享。
const (
	ProtocolOpenAIChat             = "openai_chat"
	ProtocolOpenAIResponses        = "openai_responses"
	ProtocolOpenAICodex            = "openai_codex"
	ProtocolAnthropicMessages      = "anthropic_messages"
	ProtocolAnthropicClaudeSession = "anthropic_claude_session"
	ProtocolGeminiMessages         = "gemini_messages"
	ProtocolOpenRouterChat         = "openrouter_chat"
	ProtocolBedrockInvoke          = "bedrock_invoke"
	ProtocolGrokChat               = "grok_chat"
	ProtocolKimiChat               = "kimi_chat"
	ProtocolDeepSeekChat           = "deepseek_chat"
	ProtocolMistralChat            = "mistral_chat"
	ProtocolGroqCloudChat          = "groqcloud_chat"
	ProtocolTogetherChat           = "together_chat"
	ProtocolPerplexityChat         = "perplexity_chat"
	ProtocolFireworksChat          = "fireworks_chat"
	// 国内大模型 OpenAI 兼容直通路径
	ProtocolQwenChat     = "qwen_chat"     // 通义千问 Qwen（阿里 DashScope，OpenAI 兼容）
	ProtocolGLMChat      = "glm_chat"      // 智谱 GLM/ChatGLM（BigModel，OpenAI 兼容）
	ProtocolYiChat       = "yi_chat"       // 零一万物 Yi（01.AI，OpenAI 兼容）
	ProtocolBaichuanChat = "baichuan_chat" // 百川大模型（OpenAI 兼容）
	ProtocolDoubaoChat   = "doubao_chat"   // 豆包 Doubao（火山方舟 Volcengine Ark，OpenAI 兼容）
	ProtocolErnieChat    = "ernie_chat"    // 文心 ERNIE（百度千帆 Qianfan v2，OpenAI 兼容）
	ProtocolStepChat     = "step_chat"     // 阶跃星辰 StepFun（OpenAI 兼容）
	ProtocolHunyuanChat  = "hunyuan_chat"  // 腾讯混元 Hunyuan（OpenAI 兼容端点，Bearer）
	ProtocolMinimaxChat  = "minimax_chat"  // MiniMax（api.minimax.io，OpenAI 兼容 /v1/chat/completions，Bearer）
	ProtocolCohereChat   = "cohere_chat"   // Cohere（api.cohere.ai/compatibility/v1，OpenAI 兼容，Bearer）
	ProtocolOllamaChat   = "ollama_chat"   // Ollama 自托管（OpenAI 兼容 /v1/chat/completions；默认 endpoint 仅占位，实际部署必须经 channel/account base_url 覆盖到真实主机）
	ProtocolOllamaNative = "ollama_native" // Ollama 原生 /api/chat（NDJSON 流式、options{} 采样参数；与 ollama_chat 并存，默认 endpoint 同为本机占位）
	ProtocolDifyChat     = "dify_chat"     // Dify 应用 API（chat-messages/workflows/completion-messages；per-app token，事件键 SSE）
	// Replicate 图片生成（POST /v1/models/{model}/predictions + Prefer: wait
	// 同步等待）。仅图片 lane（imageshttp /v1/images/generations）serving;
	// 刻意不注册入站 protocol_selector/stream_scanner——chat lane 误绑定本族
	// 时 MarshalToProviderRequest fail-closed(守卫:gateway.
	// TestMarshalReplicateImageFamilyFailsClosedOnChatLane)。
	ProtocolReplicateImage = "replicate_image"
	// Vertex AI serving（Google Cloud aiplatform）。两个独立 family 共享
	// "vertex" 平台与出站 SSRF 策略：vertex_gemini 走 publishers/google +
	// generateContent/streamGenerateContent（body passthrough）；vertex_anthropic
	// 走 publishers/anthropic + rawPredict/streamRawPredict（body 剥 model/stream
	// + 注 anthropic_version）。凭据 runtime 形态为 upstream_passthrough（Value
	// 已是 Bearer access_token，由 credentialworker metadata token 刷新链 materialize）。
	ProtocolVertexGemini    = "vertex_gemini"
	ProtocolVertexAnthropic = "vertex_anthropic"
	// 6 家订阅 session 反转路径（OCAW 实施前为 scaffold + TODO header）
	ProtocolCursorSession         = "cursor_session"
	ProtocolCopilotSession        = "copilot_session"
	ProtocolGeminiAdvancedSession = "gemini_advanced_session"
	ProtocolAntigravitySession    = "antigravity_session"
	ProtocolKiroSession           = "kiro_session"
	ProtocolWindsurfSession       = "windsurf_session"
)

const (
	placeholderSessionAdaptersEnv = "HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS"

	cursorSessionAdapterEnv         = "HUAKAI_ENABLE_CURSOR_SESSION_ADAPTER"
	copilotSessionAdapterEnv        = "HUAKAI_ENABLE_COPILOT_SESSION_ADAPTER"
	geminiAdvancedSessionAdapterEnv = "HUAKAI_ENABLE_GEMINI_ADVANCED_SESSION_ADAPTER"
	antigravitySessionAdapterEnv    = "HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER"
	kiroSessionAdapterEnv           = "HUAKAI_ENABLE_KIRO_SESSION_ADAPTER"
	windsurfSessionAdapterEnv       = "HUAKAI_ENABLE_WINDSURF_SESSION_ADAPTER"
)

// Build 创建注册表并注册全部已实现 vendor adapter。失败时 panic — 启动
// 期问题不应静默吞，必须 fail-loud。
func Build() *provider.StaticRegistry {
	r := provider.NewStaticRegistry()

	// OpenAI Chat Completions（v1/chat/completions）
	r.MustRegister(ProtocolOpenAIChat, &openai.PassthroughAdapter{})

	// OpenAI Responses API 仅 endpoint 区分；body / SSE shape 由 HCSF
	// translate 层处理。注册到独立 protocol family，后续切换 adapter 不
	// 影响 router 配置。
	r.MustRegister(ProtocolOpenAIResponses, &openai.PassthroughAdapter{
		Endpoint: "https://api.openai.com/v1/responses",
	})

	// OpenAI Codex CLI / ChatGPT Plus session 反转。出站到 chatgpt.com
	// 自有 backend，凭据形态为 session_token / upstream_passthrough（拒
	// apikey）。Endpoint 默认 chatgpt.com/backend-api/codex/completions，
	// 可在 adapter 字段覆盖。
	r.MustRegister(ProtocolOpenAICodex, &openai.CodexSessionAdapter{})

	r.MustRegister(ProtocolAnthropicMessages, &anthropic.PassthroughAdapter{})
	// S1-005: the provider-side OAuthSessionAdapter exists, but the
	// anthropic_claude_session serving path is not fully wired end-to-end.
	// Keep it fail-closed by default rather than exposing a half-served family.
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
	// 以下 8 家为 OpenAI Chat Completions 兼容直通路径。
	r.MustRegister(ProtocolGrokChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "grok",
		Endpoint:     "https://api.x.ai/v1/chat/completions",
	})
	r.MustRegister(ProtocolKimiChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "kimi",
		Endpoint:     "https://api.kimi.com/coding/v1/chat/completions",
	})
	r.MustRegister(ProtocolDeepSeekChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "deepseek",
		Endpoint:     "https://api.deepseek.com/v1/chat/completions",
	})
	r.MustRegister(ProtocolMistralChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "mistral",
		Endpoint:     "https://api.mistral.ai/v1/chat/completions",
	})
	r.MustRegister(ProtocolGroqCloudChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "groqcloud",
		Endpoint:     "https://api.groq.com/openai/v1/chat/completions",
	})
	r.MustRegister(ProtocolTogetherChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "together",
		Endpoint:     "https://api.together.xyz/v1/chat/completions",
	})
	r.MustRegister(ProtocolPerplexityChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "perplexity",
		Endpoint:     "https://api.perplexity.ai/chat/completions",
	})
	r.MustRegister(ProtocolFireworksChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "fireworks",
		Endpoint:     "https://api.fireworks.ai/inference/v1/chat/completions",
	})
	// 国内大模型（OpenAI 兼容直通）。各家均暴露 OpenAI 形 /chat/completions +
	// Bearer 鉴权；channel base_url 可覆盖默认 Endpoint。
	r.MustRegister(ProtocolQwenChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "qwen",
		Endpoint:     "https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions",
	})
	r.MustRegister(ProtocolGLMChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "glm",
		Endpoint:     "https://open.bigmodel.cn/api/paas/v4/chat/completions",
	})
	r.MustRegister(ProtocolYiChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "yi",
		Endpoint:     "https://api.lingyiwanwu.com/v1/chat/completions",
	})
	r.MustRegister(ProtocolBaichuanChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "baichuan",
		Endpoint:     "https://api.baichuan-ai.com/v1/chat/completions",
	})
	r.MustRegister(ProtocolDoubaoChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "doubao",
		Endpoint:     "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
	})
	r.MustRegister(ProtocolErnieChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "ernie",
		Endpoint:     "https://qianfan.baidubce.com/v2/chat/completions",
	})
	r.MustRegister(ProtocolStepChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "step",
		Endpoint:     "https://api.stepfun.com/v1/chat/completions",
	})
	r.MustRegister(ProtocolHunyuanChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "hunyuan",
		Endpoint:     "https://api.hunyuan.cloud.tencent.com/v1/chat/completions",
	})
	r.MustRegister(ProtocolMinimaxChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "minimax",
		Endpoint:     "https://api.minimax.io/v1/chat/completions",
	})
	r.MustRegister(ProtocolCohereChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "cohere",
		Endpoint:     "https://api.cohere.ai/compatibility/v1/chat/completions",
	})
	// Ollama 自托管:默认 endpoint 指向常规本机端口仅作占位,运营经 channel/
	// account base_url 覆盖到真实主机。注意私网/localhost 上游受出站 SSRF
	// 策略约束(security,运营可配白名单),本注册只提供一等 family 与协议处理。
	r.MustRegister(ProtocolOllamaChat, &provider.OpenAICompatPassthroughAdapter{
		PlatformName: "ollama",
		Endpoint:     "http://127.0.0.1:11434/v1/chat/completions",
	})
	// Ollama 原生 /api/chat：非 OpenAI 兼容形态（options{} 采样参数 + NDJSON
	// 流式 + 显式 stream 开关），走专用 adapter；与上面的 ollama_chat 并存为
	// 两个独立 protocol family，同享 "ollama" 平台与出站 SSRF 策略姿态。
	r.MustRegister(ProtocolOllamaNative, &providerollama.Adapter{})
	// Dify 应用编排平台：非 OpenAI 兼容形态（单 query 折叠 + per-app token +
	// bot_type 分端点），走专用 adapter；自托管实例经 upstream_passthrough
	// base_url 覆盖默认 https://api.dify.ai。
	r.MustRegister(ProtocolDifyChat, &providerdify.Adapter{})
	// Replicate 图片生成:OpenAI images 形请求在 adapter 内翻译为
	// {"input":{...}},model 进 URL path,Prefer: wait 同步等待(计费正确性,
	// 见 provider/replicate 包注释)。响应翻译在图片 lane(imageshttp)完成。
	r.MustRegister(ProtocolReplicateImage, &replicate.Adapter{})
	// Vertex AI serving：两个 family 共享 vertex.PassthroughAdapter，Mode 在
	// 注册时定（router 已知 protocol family，不从 model 前缀推断）。adapter 不
	// 签 JWT——消费已 materialize 的 Bearer token（upstream_passthrough）；URL
	// 含 project/location/publisher/model 模板，location 经 ^[a-z0-9-]+$ 校验 +
	// PathEscape 防 host/path 注入。
	r.MustRegister(ProtocolVertexGemini, &vertex.PassthroughAdapter{Mode: vertex.ModeGemini})
	r.MustRegister(ProtocolVertexAnthropic, &vertex.PassthroughAdapter{Mode: vertex.ModeAnthropic})

	// 6 家订阅 session 反转路径仍含未验证 placeholder endpoint。
	// 默认不注册，避免把真实 session credential 发到未确认上游；实验环境
	// 必须逐 family 显式 opt-in，不能用一个总开关一次性打开全部。
	if placeholderSessionAdapterEnabled(cursorSessionAdapterEnv) {
		r.MustRegister(ProtocolCursorSession, &cursor.CursorSessionAdapter{})
	}
	if placeholderSessionAdapterEnabled(copilotSessionAdapterEnv) {
		r.MustRegister(ProtocolCopilotSession, &copilot.CopilotSessionAdapter{})
	}
	if placeholderSessionAdapterEnabled(geminiAdvancedSessionAdapterEnv) {
		r.MustRegister(ProtocolGeminiAdvancedSession, &gemini.GeminiAdvancedSessionAdapter{})
	}
	if placeholderSessionAdapterEnabled(antigravitySessionAdapterEnv) {
		r.MustRegister(ProtocolAntigravitySession, &antigravity.AntigravitySessionAdapter{})
	}
	if placeholderSessionAdapterEnabled(kiroSessionAdapterEnv) {
		r.MustRegister(ProtocolKiroSession, &kiro.KiroSessionAdapter{})
	}
	if placeholderSessionAdapterEnabled(windsurfSessionAdapterEnv) {
		r.MustRegister(ProtocolWindsurfSession, &windsurf.WindsurfSessionAdapter{})
	}

	return r
}

func placeholderSessionAdapterEnabled(env string) bool {
	return os.Getenv(env) == "true"
}
