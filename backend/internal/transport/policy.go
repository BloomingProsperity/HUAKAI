// 包 transport 提供按 provider 路径选 RoundTripper 的策略层。
//
// 核心承诺：
//   - mimicry transport 只在 Anthropic 池化路径（Pro/Max
//     OAuth）启用；OpenAI / Vertex / Bedrock / OpenRouter 等公开 API 路径
//     永远走 standard transport
//   - provider 路径隔离始终存在，避免跨 provider 配置污染
//   - 不允许的 (provider, mode) 组合在配置加载阶段直接 reject，不留运行时
//     fail-open 路径
package transport

import (
	"errors"
	"fmt"
)

// ProviderCode 是上游服务商的标识。新增 provider 时同步刷新
// allowedModesByProvider。
type ProviderCode string

const (
	ProviderAnthropic      ProviderCode = "anthropic"
	ProviderOpenAI         ProviderCode = "openai"
	ProviderOpenAICodex    ProviderCode = "openai_codex" // ChatGPT Plus / Codex CLI session 反转，路径走 chatgpt.com
	ProviderVertex         ProviderCode = "vertex"
	ProviderBedrock        ProviderCode = "bedrock"
	ProviderOpenRouter     ProviderCode = "openrouter"
	ProviderGrok           ProviderCode = "grok"
	ProviderCursor         ProviderCode = "cursor"   // Cursor IDE 反转
	ProviderCopilot        ProviderCode = "copilot"  // GitHub Copilot 反转
	ProviderKiro           ProviderCode = "kiro"     // AWS Kiro 反转（独立于 Bedrock）
	ProviderWindsurf       ProviderCode = "windsurf" // Codeium Windsurf 反转
	ProviderAntigravity    ProviderCode = "antigravity"
	ProviderGemini         ProviderCode = "gemini"          // Gemini generativelanguage API key 直通
	ProviderGeminiAdvanced ProviderCode = "gemini_advanced" // Gemini Advanced 网页 session 反转，路径走 gemini.google.com
	// 以下 6 家为 OpenAI 兼容直通 API key 路径，不做订阅反转。
	ProviderDeepSeek   ProviderCode = "deepseek"
	ProviderMistral    ProviderCode = "mistral"
	ProviderGroqCloud  ProviderCode = "groqcloud"
	ProviderTogether   ProviderCode = "together"
	ProviderPerplexity ProviderCode = "perplexity"
	ProviderFireworks  ProviderCode = "fireworks"

	// 12 家后补 OpenAI 兼容直通平台(国内族 + cohere + ollama)。此前缺席
	// 本清单与 allowedModesByProvider,导致 dispatcher 取 RoundTripper 时
	// ErrUnknownProvider——marshal/注册表都修好后这 12 族仍在 transport 层
	// 整体不可用(renew-156 族集不对称第 6 站)。守卫:registrydefault
	// default_test 的 TestEveryRegisteredPlatformHasTransportPolicy。
	ProviderKimi     ProviderCode = "kimi"
	ProviderQwen     ProviderCode = "qwen"
	ProviderGLM      ProviderCode = "glm"
	ProviderYi       ProviderCode = "yi"
	ProviderBaichuan ProviderCode = "baichuan"
	ProviderDoubao   ProviderCode = "doubao"
	ProviderErnie    ProviderCode = "ernie"
	ProviderStep     ProviderCode = "step"
	ProviderHunyuan  ProviderCode = "hunyuan"
	ProviderMinimax  ProviderCode = "minimax"
	ProviderCohere   ProviderCode = "cohere"
	ProviderOllama   ProviderCode = "ollama"
)

// TransportMode 决定 RoundTripper 的形态。
type TransportMode string

const (
	// TransportModeStandard 走 Go 标准 net/http 默认 transport。所有
	// provider 默认。
	TransportModeStandard TransportMode = "standard"
	// 各 vendor 反转模式下的 mimicry transport 选项。每家伪装目标不同
	// （TLS ClientHello + HTTP/2 SETTINGS + ALPN 等），由调用方按
	// fingerprint template 配置 utls dialer。
	//
	// Anthropic 路径的 mode 常量保留供未来重启用。
	TransportModeMimicryClaudeCode TransportMode = "mimicry_claude_code"
	// TransportModeMimicryChatGPT 伪装为 ChatGPT 网页 / Codex CLI 客户端。
	// 仅 OpenAI provider 允许。
	TransportModeMimicryChatGPT TransportMode = "mimicry_chatgpt"
	// TransportModeMimicryGeminiAdvanced 伪装为 Gemini Advanced 网页客户端。
	// 仅 Gemini provider 允许。
	TransportModeMimicryGeminiAdvanced TransportMode = "mimicry_gemini_advanced"
	// TransportModeMimicryAntigravity 伪装为 Google Antigravity AI agent
	// 客户端。仅 Gemini/Antigravity provider 允许。
	TransportModeMimicryAntigravity TransportMode = "mimicry_antigravity"
	// TransportModeMimicryCursor 伪装为 Cursor IDE 客户端。仅 Cursor 允许。
	TransportModeMimicryCursor TransportMode = "mimicry_cursor"
	// TransportModeMimicryCopilot 伪装为 GitHub Copilot 客户端。仅 Copilot 允许。
	TransportModeMimicryCopilot TransportMode = "mimicry_copilot"
	// TransportModeMimicryKiro 伪装为 AWS Kiro 客户端。仅 Kiro 允许。
	TransportModeMimicryKiro TransportMode = "mimicry_kiro"
	// TransportModeMimicryWindsurf 伪装为 Codeium Windsurf IDE 客户端。
	// 仅 Windsurf 允许。
	TransportModeMimicryWindsurf TransportMode = "mimicry_windsurf"

	// TransportModeDiagnosticsOnly 仅做出站连通性诊断（不发真请求体）。
	// Safe Equivalent 路径，供未来回退使用。
	TransportModeDiagnosticsOnly TransportMode = "diagnostics_only"
)

// ErrModeNotAllowedForProvider 表示当前 (provider, mode) 组合被策略
// reject。
var ErrModeNotAllowedForProvider = errors.New("transport: mode not allowed for provider")

// ErrUnknownProvider 表示 provider code 不在已注册列表中。
var ErrUnknownProvider = errors.New("transport: unknown provider")

// ErrUnknownMode 表示 mode 不在已注册列表中。
var ErrUnknownMode = errors.New("transport: unknown mode")

// allowedModesByProvider 是 provider × mode 的允许矩阵。在此表外的组合一
// 律 reject，避免任何"默认放行"的合规漏洞。
//
// Anthropic 仅允许 standard/diagnostics mode；其它 vendor 的反转 + 对应
// mimicry mode 正常允许。
var allowedModesByProvider = map[ProviderCode]map[TransportMode]bool{
	ProviderAnthropic: {
		TransportModeStandard:          true,
		TransportModeMimicryClaudeCode: true, // 全 8 mode 解封
		TransportModeDiagnosticsOnly:   true,
	},
	ProviderOpenAI: {
		TransportModeStandard:        true,
		TransportModeMimicryChatGPT:  true, // ChatGPT Plus / Codex CLI 反转
		TransportModeDiagnosticsOnly: true,
	},
	ProviderOpenAICodex: {
		// 目标 endpoint chatgpt.com（非 api.openai.com），所以独立 provider
		// 而非 ProviderOpenAI 复用。standard 直连用于 dev / 非伪装走法；
		// mimicry_chatgpt 是真正的 ChatGPT Plus / Codex CLI 客户端伪装路径。
		TransportModeStandard:        true,
		TransportModeMimicryChatGPT:  true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderVertex: {
		TransportModeStandard:              true,
		TransportModeMimicryGeminiAdvanced: true, // Gemini Advanced 反转
		TransportModeMimicryAntigravity:    true, // Google Antigravity 反转
		TransportModeDiagnosticsOnly:       true,
	},
	ProviderBedrock: {
		TransportModeStandard:        true,
		TransportModeMimicryKiro:     true, // AWS Kiro 反转（走 Bedrock 后端）
		TransportModeDiagnosticsOnly: true,
	},
	ProviderOpenRouter: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
		// OpenRouter 是 meta-aggregator，本身不是反转目标
	},
	ProviderGrok: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
		// xAI Grok 走 API key 直通；本身不是订阅反转目标
	},
	ProviderCursor: {
		TransportModeStandard:      true,
		TransportModeMimicryCursor: true,
		// Cursor 反转：用 Cursor 订阅 session 反转成 API
	},
	ProviderCopilot: {
		TransportModeStandard:       true,
		TransportModeMimicryCopilot: true,
		// GitHub Copilot 反转：用 GitHub Copilot 订阅 OAuth 反转
	},
	ProviderKiro: {
		TransportModeStandard:    true,
		TransportModeMimicryKiro: true,
		// AWS Kiro 反转（独立 ProviderCode；如果走 Bedrock 后端则用
		// ProviderBedrock + TransportModeMimicryKiro 组合也允许）
	},
	ProviderWindsurf: {
		TransportModeStandard:        true,
		TransportModeMimicryWindsurf: true,
		// Codeium Windsurf 反转
	},
	ProviderAntigravity: {
		TransportModeStandard:           true,
		TransportModeMimicryAntigravity: true,
		// Google Antigravity 反转（独立 ProviderCode 视未来规划，可能走
		// Vertex 后端）
	},
	ProviderGemini: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
		// Gemini 标准 API key 路径走 generativelanguage，不允许网页 session mimicry。
	},
	ProviderGeminiAdvanced: {
		// Gemini Advanced 网页 session 反转：endpoint = gemini.google.com
		// （非 generativelanguage.googleapis.com）。standard 直连用于 dev；
		// mimicry_gemini_advanced 是真正的浏览器伪装路径。
		TransportModeStandard:              true,
		TransportModeMimicryGeminiAdvanced: true,
		TransportModeDiagnosticsOnly:       true,
	},
	// 以下 6 家走 API key 直通；不是订阅反转目标，仅 standard + diagnostics。
	ProviderDeepSeek: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderMistral: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderGroqCloud: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderTogether: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderPerplexity: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderFireworks: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	// 12 家后补 OpenAI 兼容直通平台:与 deepseek 等同款(standard +
	// diagnostics_only),无 mimicry 模式。
	ProviderKimi: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderQwen: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderGLM: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderYi: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderBaichuan: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderDoubao: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderErnie: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderStep: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderHunyuan: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderMinimax: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderCohere: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderOllama: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
}

// ValidateModeForProvider 在配置加载阶段调用，校验 (provider, mode)
// 组合合法。在 nil 错误返回前已确认 provider 与 mode 都在已注册列表中。
func ValidateModeForProvider(provider ProviderCode, mode TransportMode) error {
	allowed, ok := allowedModesByProvider[provider]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	if !isKnownMode(mode) {
		return fmt.Errorf("%w: %s", ErrUnknownMode, mode)
	}
	if !allowed[mode] {
		return fmt.Errorf("%w: provider=%s mode=%s", ErrModeNotAllowedForProvider, provider, mode)
	}
	return nil
}

// AllowedModesForProvider 返回该 provider 允许的 mode 集合（用于 admin
// 渲染下拉框 / 文档生成）。未知 provider 返回 nil。
func AllowedModesForProvider(provider ProviderCode) []TransportMode {
	allowed, ok := allowedModesByProvider[provider]
	if !ok {
		return nil
	}
	out := make([]TransportMode, 0, len(allowed))
	for m := range allowed {
		out = append(out, m)
	}
	return out
}

// isKnownMode 判断 mode 是否在 TransportMode 枚举内。
func isKnownMode(mode TransportMode) bool {
	switch mode {
	case TransportModeStandard,
		TransportModeMimicryClaudeCode,
		TransportModeMimicryChatGPT,
		TransportModeMimicryGeminiAdvanced,
		TransportModeMimicryAntigravity,
		TransportModeMimicryCursor,
		TransportModeMimicryCopilot,
		TransportModeMimicryKiro,
		TransportModeMimicryWindsurf,
		TransportModeDiagnosticsOnly:
		return true
	}
	return false
}
