package otelbridge

import (
	"context"
	"expvar"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestPrometheusExporterEnabledBridgesGroupPolicyFailOpen(t *testing.T) {
	t.Setenv("HUAKAI_METRICS_PROMETHEUS", "true")

	mp, handler, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown() error = %v", err)
		}
	}()
	if handler == nil {
		t.Fatalf("Setup() with HUAKAI_METRICS_PROMETHEUS=true returned nil handler")
	}
	if err := RegisterBridge(context.Background(), mp); err != nil {
		t.Fatalf("RegisterBridge() error = %v", err)
	}

	setExpvarInt(t, "group_policy_fail_open_total", 3)

	body := scrapeMetrics(t, handler)
	assertPromMetricValue(t, body, "huakai_group_policy_failopen_total", "3")
}

func TestPrometheusExporterEnabledBridgesGroupPolicyFailClosed(t *testing.T) {
	t.Setenv("HUAKAI_METRICS_PROMETHEUS", "true")

	mp, handler, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown() error = %v", err)
		}
	}()
	if handler == nil {
		t.Fatalf("Setup() with HUAKAI_METRICS_PROMETHEUS=true returned nil handler")
	}
	if err := RegisterBridge(context.Background(), mp); err != nil {
		t.Fatalf("RegisterBridge() error = %v", err)
	}

	setExpvarInt(t, "group_policy_fail_closed_total", 4)

	body := scrapeMetrics(t, handler)
	assertPromMetricValue(t, body, "huakai_group_policy_failclosed_total", "4")
}

func TestSetupDisabledReturnsNoopAndNoHandler(t *testing.T) {
	t.Setenv("HUAKAI_METRICS_PROMETHEUS", "")

	mp, handler, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if mp == nil {
		t.Fatalf("Setup() returned nil MeterProvider when disabled")
	}
	if handler != nil {
		t.Fatalf("Setup() returned metrics handler when env is unset")
	}
	if shutdown == nil {
		t.Fatalf("Setup() returned nil shutdown when disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("disabled shutdown() error = %v", err)
	}
}

func TestBillingMetricBridgeInOutput(t *testing.T) {
	t.Setenv("HUAKAI_METRICS_PROMETHEUS", "true")

	mp, handler, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown() error = %v", err)
		}
	}()
	if handler == nil {
		t.Fatalf("Setup() with HUAKAI_METRICS_PROMETHEUS=true returned nil handler")
	}
	if err := RegisterBridge(context.Background(), mp); err != nil {
		t.Fatalf("RegisterBridge() error = %v", err)
	}

	setExpvarMapInt(t, "billing_settings", "resolver_db_read_fail_total", 5)

	body := scrapeMetrics(t, handler)
	assertPromMetricValue(t, body, "huakai_billing_resolver_db_fail_total", "5")
}

func TestExpvarMetricSourceSnapshotsBridgeMetrics(t *testing.T) {
	// MUTATION: build the alerting snapshot from a stale hard-coded map or wrong key names; rules never see the live bridged metric value.
	setExpvarMapInt(t, "billing_settings", "resolver_db_read_fail_total", 11)
	setExpvarInt(t, "group_policy_fail_open_total", 4)

	source := NewExpvarMetricSource()
	snapshot, err := source.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := snapshot["huakai_billing_resolver_db_fail_total"]; got != 11 {
		t.Fatalf("huakai_billing_resolver_db_fail_total=%v want 11", got)
	}
	if got := snapshot["huakai_group_policy_failopen_total"]; got != 4 {
		t.Fatalf("huakai_group_policy_failopen_total=%v want 4", got)
	}
}

func scrapeMetrics(t *testing.T, handler http.Handler) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func assertPromMetricValue(t *testing.T, body, name, value string) {
	t.Helper()

	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `(?:\{[^}\n]*\})?\s+` + regexp.QuoteMeta(value) + `(?:\.0+)?$`)
	if !re.MatchString(body) {
		t.Fatalf("metrics output missing exact %s=%s; body:\n%s", name, value, body)
	}
}

func setExpvarInt(t *testing.T, name string, value int64) {
	t.Helper()

	if existing := expvar.Get(name); existing != nil {
		counter, ok := existing.(*expvar.Int)
		if !ok {
			t.Fatalf("expvar %s has type %T, want *expvar.Int", name, existing)
		}
		counter.Set(value)
		return
	}
	expvar.NewInt(name).Set(value)
}

func setExpvarMapInt(t *testing.T, mapName, key string, value int64) {
	t.Helper()

	metricMap, ok := expvar.Get(mapName).(*expvar.Map)
	if !ok {
		if existing := expvar.Get(mapName); existing != nil {
			t.Fatalf("expvar %s has type %T, want *expvar.Map", mapName, existing)
		}
		metricMap = expvar.NewMap(mapName)
	}
	if existing := metricMap.Get(key); existing != nil {
		counter, ok := existing.(*expvar.Int)
		if !ok {
			t.Fatalf("expvar %s.%s has type %T, want *expvar.Int", mapName, key, existing)
		}
		counter.Set(value)
		return
	}
	metricMap.Add(key, value)
}
