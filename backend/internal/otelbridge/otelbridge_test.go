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

// TestPrometheusExporterEnabledBridgesBudgetFailOpen 守护预算/限流强制执行的
// fail-open 计数器被桥接到 Prometheus scrape,这样当强制执行被后端错误静默绕过时,
// 运维能据此告警。
// 变异:从 bridgeCounters() 删除 huakai_budget_failopen_total 条目(或映射到
// 错误的 expvar key)-> scrape body 缺失该指标 -> assertPromMetricValue 变红。
func TestPrometheusExporterEnabledBridgesBudgetFailOpen(t *testing.T) {
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

	setExpvarInt(t, "budget_fail_open_total", 9)

	body := scrapeMetrics(t, handler)
	assertPromMetricValue(t, body, "huakai_budget_failopen_total", "9")
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
	// 变异:用陈旧的硬编码 map 或错误的 key 名构造告警快照;规则永远看不到实时桥接的指标值。
	setExpvarMapInt(t, "billing_settings", "resolver_db_read_fail_total", 11)
	setExpvarInt(t, "group_policy_fail_open_total", 4)
	setExpvarInt(t, "budget_fail_open_total", 6)

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
	// 告警路径:budget fail-open 计数器必须能到达规则求值快照。
	// 变异:移除 budget 桥接条目 -> key 缺失 -> got==0 != 6 -> 变红。
	if got := snapshot["huakai_budget_failopen_total"]; got != 6 {
		t.Fatalf("huakai_budget_failopen_total=%v want 6", got)
	}
}

// TestL2CacheMetricsBridgedToPrometheusAndAlertSnapshot 守护按 (vendor,model) 打标签的
// L2 响应缓存 expvar map 在 Prometheus scrape 和 alert-rule 快照两处都被聚合成扁平总数,
// 这样运维在启用缓存后就能对缓存命中率 / 容量告警。每个 map 用两个标签,证明桥接是
// 跨标签求和(而非只读一个)。
// 变异:从 bridgeCounters() 删除某个 L2 条目 -> 指标缺失(快照为 0 / scrape 中缺失)
// -> 变红;让 readExpvarMapSum 只读单个 key 而非求和 -> 3 或 4 != 7 -> 变红。
func TestL2CacheMetricsBridgedToPrometheusAndAlertSnapshot(t *testing.T) {
	setExpvarMapInt(t, "huakai_cache_l2_hit_total", "vendor=a,model=x", 3)
	setExpvarMapInt(t, "huakai_cache_l2_hit_total", "vendor=b,model=y", 4) // sum 7
	setExpvarMapInt(t, "huakai_cache_l2_miss_total", "vendor=a,model=x", 1)
	setExpvarMapInt(t, "huakai_cache_l2_miss_total", "vendor=b,model=y", 2) // sum 3
	setExpvarMapInt(t, "huakai_cache_l2_size_bytes", "vendor=a,model=x", 1000)
	setExpvarMapInt(t, "huakai_cache_l2_size_bytes", "vendor=b,model=y", 2000) // sum 3000

	// 告警快照这一支:alert 引擎据以求值规则的扁平 map。
	snap, err := ExpvarMetricSource{}.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snap["huakai_cache_l2_hit_total"]; got != 7 {
		t.Errorf("snapshot hit_total=%v want 7 (sum across labels)", got)
	}
	if got := snap["huakai_cache_l2_miss_total"]; got != 3 {
		t.Errorf("snapshot miss_total=%v want 3 (sum across labels)", got)
	}
	if got := snap["huakai_cache_l2_size_bytes"]; got != 3000 {
		t.Errorf("snapshot size_bytes=%v want 3000 (sum across labels)", got)
	}

	// Prometheus scrape 这一支。
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
	body := scrapeMetrics(t, handler)
	assertPromMetricValue(t, body, "huakai_cache_l2_hit_total", "7")
	assertPromMetricValue(t, body, "huakai_cache_l2_miss_total", "3")
	assertPromMetricValue(t, body, "huakai_cache_l2_size_bytes", "3000")
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
