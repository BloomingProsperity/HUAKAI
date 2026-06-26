package hermesops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

func fakeAlertRules() []alerting.AlertRule {
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fired := time.Date(2026, 6, 26, 8, 0, 0, 0, time.UTC)
	return []alerting.AlertRule{
		{
			ID: 1, TenantID: 7, Name: "high-error-rate", Metric: "error_rate",
			MetricType: "ratio", Comparator: "gt", Threshold: 0.5, Severity: "critical",
			WindowSeconds: 300, SustainedSeconds: 60, CooldownSeconds: 900,
			NotifyEmail: true, Filters: map[string]string{"provider_account_id": "5"},
			Enabled: true, LastTriggeredAt: &fired, CreatedAt: t0, UpdatedAt: t0,
		},
		{
			ID: 2, TenantID: 7, Name: "slow-latency", Metric: "p95_latency_ms",
			MetricType: "gauge", Comparator: "gt", Threshold: 2000, Severity: "warning",
			WindowSeconds: 600, Enabled: false, CreatedAt: t0, UpdatedAt: t0,
		},
	}
}

func TestAlertRuleListSpec(t *testing.T) {
	deps := AlertRuleListDeps{
		List: func(_ context.Context, in alerting.ListRulesInput) ([]alerting.AlertRule, error) {
			if in.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", in.TenantID)
			}
			if in.Limit != alertRuleListLimit {
				t.Fatalf("Limit 应为 alertRuleListLimit=%d, got %d", alertRuleListLimit, in.Limit)
			}
			return fakeAlertRules(), nil
		},
	}
	spec := AlertRuleListSpec(deps)

	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["rule_count"].(int) != 2 {
		t.Fatalf("rule_count 应 2, got %v", res.Summary["rule_count"])
	}
	if res.Summary["enabled_count"].(int) != 1 {
		t.Fatalf("enabled_count 应 1, got %v", res.Summary["enabled_count"])
	}
	bySev := res.Summary["by_severity"].(map[string]any)
	if bySev["critical"] != 1 || bySev["warning"] != 1 {
		t.Fatalf("by_severity 错: %v", bySev)
	}

	items := res.Summary["items"].([]map[string]any)
	r0 := items[0]
	if r0["name"] != "high-error-rate" || r0["metric"] != "error_rate" {
		t.Fatalf("rule[0] 投影错: %v", r0)
	}
	if r0["comparator"] != "gt" || r0["severity"] != "critical" || r0["threshold"].(float64) != 0.5 {
		t.Fatalf("rule[0] 比较/严重度/阈值投影错: %v", r0)
	}
	if r0["metric_type"] != "ratio" || r0["notify_email"] != true {
		t.Fatalf("rule[0] metric_type/notify_email 错: %v", r0)
	}
	// Filters 是运营自填的规则定义标签,有意投出(拷贝)。
	filters := r0["filters"].(map[string]string)
	if filters["provider_account_id"] != "5" {
		t.Fatalf("filters 应含 provider_account_id=5: %v", filters)
	}
	// last_triggered_at 有值;第二条无→nil。
	if r0["last_triggered_at"] == nil {
		t.Fatalf("rule[0] 应有 last_triggered_at")
	}
	if items[1]["last_triggered_at"] != nil {
		t.Fatalf("rule[1] 未触发应 last_triggered_at=nil, got %v", items[1]["last_triggered_at"])
	}

	// 不回投 tenant_id。
	if _, has := r0["tenant_id"]; has {
		t.Fatalf("不应投影 tenant_id: %v", r0)
	}
}

// filters 必须是拷贝:改返回的 map 不应影响源(防共享底层引用)。
func TestAlertRuleListFiltersAreCopied(t *testing.T) {
	src := []alerting.AlertRule{
		{ID: 1, TenantID: 7, Name: "r", Filters: map[string]string{"k": "v"}, Enabled: true},
	}
	deps := AlertRuleListDeps{
		List: func(_ context.Context, _ alerting.ListRulesInput) ([]alerting.AlertRule, error) {
			return src, nil
		},
	}
	res, err := AlertRuleListSpec(deps).Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := res.Summary["items"].([]map[string]any)[0]["filters"].(map[string]string)
	out["k"] = "MUTATED"
	if src[0].Filters["k"] != "v" {
		t.Fatalf("filters 未拷贝:改投影输出污染了源 map(got %q)", src[0].Filters["k"])
	}
}

func TestAlertRuleListNilDep(t *testing.T) {
	_, err := AlertRuleListSpec(AlertRuleListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestAlertRuleListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := AlertRuleListDeps{
		List: func(_ context.Context, _ alerting.ListRulesInput) ([]alerting.AlertRule, error) {
			return nil, sentinel
		},
	}
	_, err := AlertRuleListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
