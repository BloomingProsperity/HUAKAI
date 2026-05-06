// 包 transport — RoundTripper 工厂：按 provider + mode 选具体 transport。
//
// R3 transport mimicry 实施完成前，mimicry / diagnostics_only 两个 mode
// 调用方会拿到明确的 ErrTransportNotImplemented，方便 admin 知道为啥配置
// 加载没问题但实际请求失败。
package transport

import (
	"errors"
	"net/http"
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
	// mimicry 是 R3 transport mimicry 用的 RoundTripper。nil 表示尚未
	// 实施（当前默认）。
	mimicry http.RoundTripper
	// diagnostics 是仅做连通性诊断的 RoundTripper。nil 表示尚未实施。
	diagnostics http.RoundTripper
}

// NewFactory 构造一个新的 Factory。所有 RoundTripper 字段为 nil — 调用
// SetXxx 注入实例。standard 在未注入时回落到 http.DefaultTransport。
func NewFactory() *Factory {
	return &Factory{}
}

// SetStandard 注入 standard RoundTripper（覆盖 http.DefaultTransport
// 默认）。生产环境通常不调；测试或自定义 dialer 时用。
func (f *Factory) SetStandard(rt http.RoundTripper) {
	f.standard = rt
}

// SetMimicry 注入 R3 transport mimicry RoundTripper。R3 实施时调一次。
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
		if f.standard != nil {
			return f.standard, nil
		}
		return http.DefaultTransport, nil
	case TransportModeMimicryClaudeCode:
		if f.mimicry != nil {
			return f.mimicry, nil
		}
		return nil, ErrTransportNotImplemented
	case TransportModeDiagnosticsOnly:
		if f.diagnostics != nil {
			return f.diagnostics, nil
		}
		return nil, ErrTransportNotImplemented
	}
	// ValidateModeForProvider 已确保 mode 已知，理论不可达。
	return nil, ErrUnknownMode
}
