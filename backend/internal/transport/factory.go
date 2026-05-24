// 包 transport — RoundTripper 工厂：按 provider + mode 选具体 transport。
//
// R3 transport mimicry Phase A 已接入 uTLS dialer；diagnostics_only 仍保留
// fail-loud 占位，避免调用方误以为诊断路径已经完整实现。
package transport

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// ErrTransportNotImplemented 表示 (provider, mode) 组合策略允许但具体
// RoundTripper 还没实现（R3 / diagnostics 还在路径上）。
var ErrTransportNotImplemented = errors.New("transport: round-tripper not yet implemented for this mode")

// Factory 持有可选的非默认 RoundTripper 实例。零值即可使用，默认 standard
// 路径走 http.DefaultTransport 的 Clone 并显式 Proxy=nil（见
// standardRoundTripper），以剥离 HTTP_PROXY/HTTPS_PROXY env 对账号绑定
// 代理隔离的破坏。
type Factory struct {
	// standard 是 standard mode 用的 RoundTripper。nil 时回落到
	// fallback：http.DefaultTransport.Clone() 并把 Proxy 设为 nil，
	// 任何代理只能通过 dispatcher.applyProxy 显式绑定生效。
	standard http.RoundTripper
	// standardOnce + standardCached 让 fallback transport 单例化，
	// 保留 connection pool 复用（http.Transport 重复 new 会让池作废）。
	standardOnce   sync.Once
	standardCached http.RoundTripper
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
		tmpl, err := f.mimicryTemplate(mode)
		if err != nil {
			return nil, err
		}
		return f.mimicryRoundTripper(mode, tmpl), nil
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
	// 关键安全约束：http.DefaultTransport.Proxy 默认值是 ProxyFromEnvironment，
	// 会读 HTTP_PROXY / HTTPS_PROXY / NO_PROXY env。Docker / 部署环境只要
	// 这些变量存在，所有"未绑定代理"的账号会被全局代理截胡，破坏按账号
	// 绑定 IP / 代理的隔离设计（dispatcher.applyProxy → ProxyResolver 才是
	// HUAKAI 唯一允许的代理决策点）。
	//
	// 这里 clone 一份 DefaultTransport 并显式 Proxy=nil，让 standard 路径
	// 默认直连；外部代理只能通过 dispatcher.applyProxy 显式 wrap 进来。
	// 单例化（standardOnce）避免重复 new *http.Transport 让 connection
	// pool 失效。
	f.standardOnce.Do(func() {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			// 保底：Go runtime 若被替换 DefaultTransport 为非 *http.Transport
			// （极少见，比如 test 自打补丁），仍构造一个最小 *http.Transport
			// 防止账号 IP 隔离被绕过。
			f.standardCached = &http.Transport{Proxy: nil}
			return
		}
		cloned := base.Clone()
		cloned.Proxy = nil
		f.standardCached = cloned
	})
	return f.standardCached
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

// mimicryTemplate 返回 mode 对应的 per-mode 指纹模板; 缺失或 stub 时返
// (nil, err) 让 caller fail-closed, 不回退到 Anthropic Phase A 默认模板。
// 否则 kiro / chatgpt / gemini_advanced 等模式会用 Anthropic JA3 出站,
// 反检测目标完全失效。
//
// HUAKAI_TRANSPORT_PHASE_A_FALLBACK=true 仅留给 explicit opt-in 测试/调试,
// 生产默认 fail-closed; 没注入 templateRegistry 也算配置缺失, reject。
func (f *Factory) mimicryTemplate(mode TransportMode) (*mimicry.ClientHelloTemplate, error) {
	phaseAOptIn := os.Getenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK") == "true"
	if f.templateRegistry == nil {
		slog.Warn("transport mimicry template registry missing", "mode", mode, "reason_class", "registry_missing")
		if phaseAOptIn {
			return mimicry.PhaseADefaultTemplate(), nil
		}
		return nil, fmt.Errorf("transport: mimicry template registry not configured for mode=%s", mode)
	}
	tmpl, ok := f.templateRegistry.Lookup(mimicry.TransportMode(mode))
	if !ok {
		slog.Warn("transport mimicry template missing", "mode", mode, "reason_class", "template_missing")
		if phaseAOptIn {
			return mimicry.PhaseADefaultTemplate(), nil
		}
		return nil, fmt.Errorf("transport: mimicry template missing for mode=%s", mode)
	}
	if tmpl.IsStub() {
		slog.Warn("transport mimicry template stub", "mode", mode, "reason_class", "template_stub")
		if phaseAOptIn {
			return mimicry.PhaseADefaultTemplate(), nil
		}
		return nil, fmt.Errorf("transport: mimicry template stub for mode=%s (not production-ready)", mode)
	}
	return tmpl, nil
}
