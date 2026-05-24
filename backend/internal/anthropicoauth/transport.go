package anthropicoauth

import (
	"log/slog"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

const (
	AnthropicMimicryProfileID = mimicry.SidecarProfileAnthropicCLIMimicryV1
)

func DefaultHTTPClient() *http.Client {
	return mimicryHTTPClient(nil, nil, "anthropic_oauth")
}

func mimicryHTTPClient(factory *transport.Factory, registry *mimicry.TemplateRegistry, scope string) *http.Client {
	if factory == nil {
		if registry == nil {
			registry = mimicry.NewDefaultTemplateRegistry()
		}
		factory = transport.NewFactory(registry)
	}
	rt, err := factory.For(transport.ProviderAnthropic, transport.TransportModeMimicryClaudeCode)
	if err != nil {
		slog.Warn("anthropicoauth mimicry transport unavailable",
			"reason_class", "mimicry_transport_unavailable",
			"profile", AnthropicMimicryProfileID,
			"mode", transport.TransportModeMimicryClaudeCode,
			"scope", scope,
			"error", err,
		)
		std, stdErr := transport.NewFactory().For(transport.ProviderAnthropic, transport.TransportModeStandard)
		if stdErr != nil {
			return &http.Client{Transport: http.DefaultTransport}
		}
		return &http.Client{Transport: std}
	}
	return &http.Client{Transport: rt}
}
