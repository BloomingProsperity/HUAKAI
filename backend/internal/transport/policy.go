// 包 transport 提供按 provider 路径选 RoundTripper 的策略层。
//
// 核心承诺：
//   - mimicry transport（R3 强伪装层）只在 Anthropic 池化路径（Pro/Max
//     OAuth）启用；OpenAI / Vertex / Bedrock / OpenRouter 等公开 API 路径
//     永远走 standard transport
//   - 这是"lane 一致项"（详见 docs/plans/2026-05-06-r3-transport-mimicry-
//     synthesis.md §5）：不论 R3 最终是否上线，provider 路径隔离都需要
//     有，避免跨 provider 配置污染
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
	ProviderAnthropic  ProviderCode = "anthropic"
	ProviderOpenAI     ProviderCode = "openai"
	ProviderVertex     ProviderCode = "vertex"
	ProviderBedrock    ProviderCode = "bedrock"
	ProviderOpenRouter ProviderCode = "openrouter"
)

// TransportMode 决定 RoundTripper 的形态。
type TransportMode string

const (
	// TransportModeStandard 走 Go 标准 net/http 默认 transport。所有
	// provider 默认。
	TransportModeStandard TransportMode = "standard"
	// TransportModeMimicryClaudeCode 走 R3 transport mimicry：自定义
	// TLS ClientHello + HTTP/2 SETTINGS 与 Claude Code CLI 一致。**仅
	// Anthropic provider 允许**。
	TransportModeMimicryClaudeCode TransportMode = "mimicry_claude_code"
	// TransportModeDiagnosticsOnly 仅做出站连通性诊断（不发真请求体）。
	// Codex lane plan 提议的 Safe Equivalent 路径，未来 R3 实施时回退用。
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
var allowedModesByProvider = map[ProviderCode]map[TransportMode]bool{
	ProviderAnthropic: {
		TransportModeStandard:          true,
		TransportModeMimicryClaudeCode: true, // 唯一允许 mimicry 的 provider
		TransportModeDiagnosticsOnly:   true,
	},
	ProviderOpenAI: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
		// 显式不含 mimicry — OpenAI 公开 API 不需要也不应伪装
	},
	ProviderVertex: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderBedrock: {
		TransportModeStandard:        true,
		TransportModeDiagnosticsOnly: true,
	},
	ProviderOpenRouter: {
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
	case TransportModeStandard, TransportModeMimicryClaudeCode, TransportModeDiagnosticsOnly:
		return true
	}
	return false
}
