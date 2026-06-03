package otelbridge

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const prometheusMetricsEnv = "HUAKAI_METRICS_PROMETHEUS"

// Setup builds the metrics export pipeline. It is intentionally default-off:
// only HUAKAI_METRICS_PROMETHEUS=true creates a scrape handler.
func Setup(_ context.Context) (otelmetric.MeterProvider, http.Handler, func(context.Context) error, error) {
	if !prometheusEnabled() {
		return metricnoop.NewMeterProvider(), nil, func(context.Context) error { return nil }, nil
	}

	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		otelprom.WithoutCounterSuffixes(),
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return provider, handler, provider.Shutdown, nil
}

func prometheusEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(prometheusMetricsEnv)), "true")
}
