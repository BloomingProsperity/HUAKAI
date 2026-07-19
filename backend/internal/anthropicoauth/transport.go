package anthropicoauth

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

const AnthropicMimicryProfileID = mimicry.SidecarProfileAnthropicCLIMimicryV1

// NewHTTPClient 从生产 transport 工厂构造 Anthropic OAuth 客户端。
// sidecar 未就绪时直接返回错误，供启动链 fail-loud。
func NewHTTPClient(factory *transport.Factory) (*http.Client, error) {
	if factory == nil {
		return nil, fmt.Errorf("anthropicoauth: transport factory missing")
	}
	rt, err := factory.For(transport.ProviderAnthropic, transport.TransportModeMimicryClaudeCode)
	if err != nil {
		return nil, fmt.Errorf("anthropicoauth: Rust mimicry transport unavailable: %w", err)
	}
	return &http.Client{Transport: rt}, nil
}

// HTTPClient 返回只会走 Rust sidecar 的客户端。该兼容入口用于尚未持有生产
// 工厂的独立组件；配置错误会变成固定失败 transport，不会回退标准 TLS。
func HTTPClient(factory *transport.Factory) *http.Client {
	client, err := NewHTTPClient(factory)
	if err == nil {
		return client
	}
	slog.Error("anthropicoauth Rust transport unavailable",
		"reason_class", "mimicry_transport_unavailable",
		"profile", AnthropicMimicryProfileID,
		"error", err,
	)
	return &http.Client{Transport: failedRoundTripper{err: err}}
}

// DefaultHTTPClient 仅作为无依赖注入调用点的 fail-closed 兼容入口。
// 生产 wiring 使用 NewHTTPClient 并在启动时验证 sidecar。
func DefaultHTTPClient() *http.Client {
	factory := transport.NewFactory()
	factory.SidecarSocketPath = strings.TrimSpace(os.Getenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET"))
	if factory.SidecarSocketPath == "" {
		factory.SidecarSocketPath = transport.DefaultSidecarSocketPath
	}
	return HTTPClient(factory)
}

type failedRoundTripper struct {
	err error
}

func (f failedRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}
