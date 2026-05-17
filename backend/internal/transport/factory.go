// 包 transport — RoundTripper 工厂：按 provider + mode 选具体 transport。
//
// R3 transport mimicry Phase A 已接入 uTLS dialer；diagnostics_only 仍保留
// fail-loud 占位，避免调用方误以为诊断路径已经完整实现。
package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// ErrTransportNotImplemented 表示 (provider, mode) 组合策略允许但具体
// RoundTripper 还没实现（R3 / diagnostics 还在路径上）。
var ErrTransportNotImplemented = errors.New("transport: round-tripper not yet implemented for this mode")

// Factory 持有可选的非默认 RoundTripper 实例。零值即可使用，默认走
// http.DefaultTransport。
type Factory struct {
	// standard 是 standard mode 用的 RoundTripper。nil 时回落到
	// http.DefaultTransport。注入便于测试。
	standard http.RoundTripper
	// mimicry 是测试或外部装配注入点。nil 时按 registry 查 per-mode 模板。
	mimicry http.RoundTripper
	// templateRegistry 保存 Phase B per-mode ClientHello 模板。
	templateRegistry *mimicry.TemplateRegistry
	mimicryMu        sync.Mutex
	mimicryByMode    map[TransportMode]http.RoundTripper
	// diagnostics 是仅做连通性诊断的 RoundTripper。nil 表示尚未实施。
	diagnostics http.RoundTripper
}

// NewFactory 构造一个新的 Factory。所有 RoundTripper 字段为 nil — 调用
// SetXxx 注入实例。standard 在未注入时回落到 http.DefaultTransport。
func NewFactory(registries ...*mimicry.TemplateRegistry) *Factory {
	var registry *mimicry.TemplateRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return &Factory{
		templateRegistry: registry,
		mimicryByMode:    make(map[TransportMode]http.RoundTripper),
	}
}

// SetStandard 注入 standard RoundTripper（覆盖 http.DefaultTransport
// 默认）。生产环境通常不调；测试或自定义 dialer 时用。
func (f *Factory) SetStandard(rt http.RoundTripper) {
	f.standard = rt
}

// SetMimicry 注入 R3 transport mimicry RoundTripper，主要供测试和未来
// per-mode 路由器使用。
func (f *Factory) SetMimicry(rt http.RoundTripper) {
	f.mimicry = rt
}

// SetDiagnostics 注入仅诊断的 RoundTripper。
func (f *Factory) SetDiagnostics(rt http.RoundTripper) {
	f.diagnostics = rt
}

// For 按 (provider, mode) 取 RoundTripper。
//
// 顺序：先 ValidateModeForProvider；通过后再按 mode 选 RoundTripper。
//
// 返回 ErrModeNotAllowedForProvider 表示策略 reject（如 OpenAI 路径请求
// mimicry mode）。返回 ErrTransportNotImplemented 表示策略允许但 transport
// 尚未实施。
func (f *Factory) For(provider ProviderCode, mode TransportMode) (http.RoundTripper, error) {
	if err := ValidateModeForProvider(provider, mode); err != nil {
		return nil, err
	}
	switch mode {
	case TransportModeStandard:
		return f.standardRoundTripper(), nil
	case TransportModeMimicryClaudeCode,
		TransportModeMimicryChatGPT,
		TransportModeMimicryGeminiAdvanced,
		TransportModeMimicryAntigravity,
		TransportModeMimicryCursor,
		TransportModeMimicryCopilot,
		TransportModeMimicryKiro,
		TransportModeMimicryWindsurf:
		if f.mimicry != nil {
			return f.mimicry, nil
		}
		return f.mimicryRoundTripper(mode, f.mimicryTemplate(mode)), nil
	case TransportModeDiagnosticsOnly:
		if f.diagnostics != nil {
			return f.diagnostics, nil
		}
		return nil, ErrTransportNotImplemented
	}
	// ValidateModeForProvider 已确保 mode 已知，理论不可达。
	return nil, ErrUnknownMode
}

func (f *Factory) standardRoundTripper() http.RoundTripper {
	if f.standard != nil {
		return f.standard
	}
	return http.DefaultTransport
}

func (f *Factory) mimicryRoundTripper(mode TransportMode, tmpl *mimicry.ClientHelloTemplate) http.RoundTripper {
	f.mimicryMu.Lock()
	defer f.mimicryMu.Unlock()
	if f.mimicryByMode == nil {
		f.mimicryByMode = make(map[TransportMode]http.RoundTripper)
	}
	if rt := f.mimicryByMode[mode]; rt != nil {
		return rt
	}
	rt := mimicry.NewRoundTripper(tmpl)
	f.mimicryByMode[mode] = rt
	return rt
}

func (f *Factory) mimicryTemplate(mode TransportMode) *mimicry.ClientHelloTemplate {
	if f.templateRegistry != nil {
		tmpl, ok := f.templateRegistry.Lookup(mimicry.TransportMode(mode))
		if ok && !tmpl.IsStub() {
			return tmpl
		}
		if ok {
			slog.Warn("transport mimicry template stub", "mode", mode, "reason_class", "template_stub")
		} else {
			slog.Warn("transport mimicry template missing", "mode", mode, "reason_class", "template_missing")
		}
	}
	// Phase A：8 个 mimicry mode 先共享 Anthropic 样本模板，Phase B
	// 改为 templates/<mode-name>.json 的 per-mode 指纹。
	return mimicry.PhaseADefaultTemplate()
}
