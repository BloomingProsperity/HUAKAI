// 包 transport 提供按 provider 与 mode 选择出口的 RoundTripper 工厂。
package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// ErrTransportNotImplemented 表示 (provider, mode) 组合策略允许但具体
// RoundTripper 还没实现（diagnostics 还在路径上）。
var ErrTransportNotImplemented = errors.New("transport: round-tripper not yet implemented for this mode")

// ErrStandardH1TransportRequired 表示部署注入了不可克隆的标准出站包装器，
// 但没有同时提供 H1 版本。此时必须明确失败，禁止绕过既有代理或日志策略直连。
var ErrStandardH1TransportRequired = errors.New("transport: dedicated standard H1 round-tripper required")

type TransportErrorClass string

const (
	TransportErrorClassSidecarUnavailable        TransportErrorClass = "sidecar_unavailable"
	TransportErrorClassSidecarProfileUnavailable TransportErrorClass = "sidecar_profile_unavailable"
)

type TransportError struct {
	Class      TransportErrorClass
	Mode       TransportMode
	SocketPath string
	Err        error
}

func (e *TransportError) Error() string {
	if e == nil {
		return "transport: unknown transport error"
	}
	base := fmt.Sprintf("transport: %s for mode=%s socket=%s", e.Class, e.Mode, e.SocketPath)
	if e.Err == nil {
		return base
	}
	return base + ": " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func TransportErrorClassOf(err error) TransportErrorClass {
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		return transportErr.Class
	}
	return ""
}

// Factory 持有可选的非默认 RoundTripper 实例。零值即可使用，默认 standard
// 路径走 http.DefaultTransport 的 Clone 并显式 Proxy=nil（见
// standardRoundTripper），以剥离 HTTP_PROXY/HTTPS_PROXY env 对账号绑定
// 代理隔离的破坏。
type Factory struct {
	// SidecarSocketPath 是 mimicry 模式唯一允许使用的 Rust/BoringSSL 出口。
	// 为空或不可用时必须拒绝请求，不能降级为标准 TLS。
	SidecarSocketPath string
	// SidecarForceH1 显式收窄 Rust sidecar 的 ALPN 到 http/1.1。nil 时按 profile
	// 的真实 ALPN 工作；它是部署兼容开关，不是默认指纹策略。
	SidecarForceH1 *bool
	// standard 是 standard mode 用的 RoundTripper。nil 时回落到
	// fallback：http.DefaultTransport.Clone() 并把 Proxy 设为 nil，
	// 任何代理只能通过 dispatcher.applyProxy 显式绑定生效。
	standard http.RoundTripper
	// standardOnce + standardCached 让 fallback transport 单例化，
	// 保留 connection pool 复用（http.Transport 重复 new 会让池作废）。
	standardOnce   sync.Once
	standardCached http.RoundTripper
	// standardH1 是标准 TLS + HTTP/1.1 的独立连接池，不能与允许协商 H2
	// 的 standard 连接复用。
	standardH1Once   sync.Once
	standardH1Cached http.RoundTripper
	standardH1       http.RoundTripper
	standardH1Err    error
	// sidecarTestOverride 只供跨包单元测试验证模式选择，不参与生产 wiring。
	// 生产代码没有配置入口，真实路径仍必须通过 Unix socket 探测 Rust sidecar。
	sidecarTestOverride  http.RoundTripper
	sidecarMu            sync.Mutex
	sidecarByMode        map[TransportMode]http.RoundTripper
	sidecarFailureByMode map[TransportMode]sidecarFailureCache
	// sidecarProbeTimeout 限定启动时/请求时的 sidecar 就绪检查时长。
	// 为零则使用 defaultSidecarProbeTimeout。
	sidecarProbeTimeout    time.Duration
	sidecarFailureCacheTTL time.Duration
	sidecarProbe           func(context.Context, string, mimicry.TransportMode, string) error
	// diagnostics 是仅做连通性诊断的 RoundTripper。nil 表示尚未实施。
	diagnostics http.RoundTripper
}

const defaultSidecarProbeTimeout = 5 * time.Second
const defaultSidecarFailureCacheTTL = time.Second

// DefaultSidecarSocketPath 是单镜像内 gateway 与 sidecar 的共享运行时合同。
const DefaultSidecarSocketPath = "/run/huakai/tls-sidecar.sock"

type sidecarFailureCache struct {
	err       error
	expiresAt time.Time
}

// NewFactory 构造一个新的 Factory。standard 在未注入时使用隔离环境代理的
// net/http transport；所有 mimicry 模式只会走 Rust sidecar。
func NewFactory() *Factory {
	return &Factory{
		sidecarByMode: make(map[TransportMode]http.RoundTripper),
	}
}

// SetStandard 注入 standard RoundTripper（覆盖 http.DefaultTransport
// 默认）。生产环境通常不调；测试或自定义 dialer 时用。
func (f *Factory) SetStandard(rt http.RoundTripper) {
	f.standard = rt
}

// SetStandardH1 注入标准 H1 RoundTripper，仅供测试或定制拨号器使用。
func (f *Factory) SetStandardH1(rt http.RoundTripper) {
	f.standardH1 = rt
}

// SetSidecarForTesting 注入仅供单元测试使用的 sidecar 替身。
// 生产构造链不得调用；仓库检查会约束调用点只存在于 *_test.go。
func (f *Factory) SetSidecarForTesting(rt http.RoundTripper) {
	f.sidecarTestOverride = rt
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
	case TransportModeStandardH1:
		return f.standardH1RoundTripper()
	case TransportModeMimicryClaudeCode,
		TransportModeMimicryChatGPT,
		TransportModeMimicryGeminiAdvanced,
		TransportModeMimicryAntigravity,
		TransportModeMimicryCursor,
		TransportModeMimicryCopilot,
		TransportModeMimicryKiro,
		TransportModeMimicryWindsurf:
		if f.sidecarTestOverride != nil {
			return f.sidecarTestOverride, nil
		}
		if f.SidecarSocketPath == "" {
			err := newSidecarTransportError(
				TransportErrorClassSidecarUnavailable,
				mode,
				"",
				fmt.Errorf("%w: empty socket path", mimicry.ErrSidecarUnavailable),
			)
			mimicry.RecordEgressProbeFailure(false)
			return nil, err
		}
		return f.sidecarRoundTripper(mode)
	case TransportModeDiagnosticsOnly:
		if f.diagnostics != nil {
			return f.diagnostics, nil
		}
		return nil, ErrTransportNotImplemented
	}
	// ValidateModeForProvider 已确保 mode 已知，理论不可达。
	return nil, ErrUnknownMode
}

func (f *Factory) standardH1RoundTripper() (http.RoundTripper, error) {
	if f.standardH1 != nil {
		return f.standardH1, nil
	}
	f.standardH1Once.Do(func() {
		base, ok := f.standardRoundTripper().(*http.Transport)
		if !ok {
			f.standardH1Err = fmt.Errorf("%w: configured standard transport type %T", ErrStandardH1TransportRequired, f.standardRoundTripper())
			return
		}
		cloned := base.Clone()
		cloned.Proxy = nil
		cloned.ForceAttemptHTTP2 = false
		cloned.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		if cloned.TLSClientConfig == nil {
			cloned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			cloned.TLSClientConfig = cloned.TLSClientConfig.Clone()
		}
		cloned.TLSClientConfig.NextProtos = []string{"http/1.1"}
		f.standardH1Cached = cloned
	})
	return f.standardH1Cached, f.standardH1Err
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
			// DM-17:网关型负载下 Go 默认 MaxIdleConnsPerHost=2 让连接
			// 复用近乎失效(每 vendor 端点只留 2 条空闲连接,高并发下
			// 反复 TLS 握手)。显式调到 64/256,IdleConnTimeout 与
			// DefaultTransport 对齐 90s。
			f.standardCached = &http.Transport{
				Proxy:               nil,
				MaxIdleConns:        256,
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     90 * time.Second,
			}
			return
		}
		cloned := base.Clone()
		cloned.Proxy = nil
		// DM-17:同上,Clone 继承 DefaultTransport 的 MaxIdleConnsPerHost
		// 零值(=2),必须显式覆盖。
		cloned.MaxIdleConns = 256
		cloned.MaxIdleConnsPerHost = 64
		f.standardCached = cloned
	})
	return f.standardCached
}

func (f *Factory) sidecarRoundTripper(mode TransportMode) (http.RoundTripper, error) {
	f.sidecarMu.Lock()
	defer f.sidecarMu.Unlock()
	if f.sidecarByMode == nil {
		f.sidecarByMode = make(map[TransportMode]http.RoundTripper)
	}
	if rt := f.sidecarByMode[mode]; rt != nil {
		return rt, nil
	}
	if cached, ok := f.sidecarFailureByMode[mode]; ok {
		if time.Now().Before(cached.expiresAt) {
			return nil, cached.err
		}
		delete(f.sidecarFailureByMode, mode)
	}
	sidecarMode := mimicry.TransportMode(mode)
	profileID, ok := mimicry.SidecarProfileForMode(sidecarMode)
	if !ok {
		err := fmt.Errorf("%w: no profile for mode %s", mimicry.ErrSidecarProfileUnavailable, mode)
		return nil, newSidecarTransportError(TransportErrorClassSidecarProfileUnavailable, mode, f.SidecarSocketPath, err)
	}
	timeout := f.sidecarProbeTimeout
	if timeout <= 0 {
		timeout = defaultSidecarProbeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	probe := f.sidecarProbe
	if probe == nil {
		probe = func(ctx context.Context, socketPath string, mode mimicry.TransportMode, _ string) error {
			return mimicry.ProbeSidecarForMode(ctx, socketPath, mode)
		}
	}
	if err := probe(ctx, f.SidecarSocketPath, sidecarMode, profileID); err != nil {
		class := classifySidecarError(err)
		// probe 失败 = 出口在真实拨号前就转不出去(默认 fail-closed 下 sidecar 宕机的主路径)。
		// 计入与 DialTLS 同一套 result 桶,否则出口成功率分母漏掉最主要的宕机情形。
		mimicry.RecordEgressProbeFailure(class == TransportErrorClassSidecarProfileUnavailable)
		slog.Warn("transport mimicry sidecar unavailable",
			"mode", mode,
			"socket_path", f.SidecarSocketPath,
			"profile_id", profileID,
			"reason_class", class,
			"error", err,
		)
		transportErr := newSidecarTransportError(class, mode, f.SidecarSocketPath, err)
		f.cacheSidecarFailureLocked(mode, transportErr)
		return nil, transportErr
	}
	var (
		rt  http.RoundTripper
		err error
	)
	if f.SidecarForceH1 != nil {
		rt, err = mimicry.NewSidecarRoundTripperForModeForceH1(f.SidecarSocketPath, sidecarMode, *f.SidecarForceH1)
	} else {
		rt, err = mimicry.NewSidecarRoundTripperForMode(f.SidecarSocketPath, sidecarMode)
	}
	if err != nil {
		transportErr := newSidecarTransportError(classifySidecarError(err), mode, f.SidecarSocketPath, err)
		f.cacheSidecarFailureLocked(mode, transportErr)
		return nil, transportErr
	}
	delete(f.sidecarFailureByMode, mode)
	f.sidecarByMode[mode] = rt
	return rt, nil
}

func (f *Factory) cacheSidecarFailureLocked(mode TransportMode, err error) {
	if err == nil {
		return
	}
	ttl := f.sidecarFailureCacheTTL
	if ttl <= 0 {
		ttl = defaultSidecarFailureCacheTTL
	}
	if f.sidecarFailureByMode == nil {
		f.sidecarFailureByMode = make(map[TransportMode]sidecarFailureCache)
	}
	f.sidecarFailureByMode[mode] = sidecarFailureCache{err: err, expiresAt: time.Now().Add(ttl)}
}

func newSidecarTransportError(class TransportErrorClass, mode TransportMode, socketPath string, err error) *TransportError {
	if class == "" {
		class = TransportErrorClassSidecarUnavailable
	}
	return &TransportError{
		Class:      class,
		Mode:       mode,
		SocketPath: socketPath,
		Err:        err,
	}
}

func classifySidecarError(err error) TransportErrorClass {
	var transportErr *TransportError
	if errors.As(err, &transportErr) && transportErr.Class != "" {
		return transportErr.Class
	}
	if errors.Is(err, mimicry.ErrSidecarProfileUnavailable) {
		return TransportErrorClassSidecarProfileUnavailable
	}
	return TransportErrorClassSidecarUnavailable
}
