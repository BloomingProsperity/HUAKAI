package hermesops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

func fakeAlertEvents() []alerting.AlertEvent {
	fired := time.Date(2026, 6, 26, 8, 0, 0, 0, time.UTC)
	resolved := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)
	thr := 0.5
	mv := 0.73
	return []alerting.AlertEvent{
		{
			ID: 100, TenantID: 7, RuleID: 1, State: "firing",
			ObservedValue: 0.73, ThresholdValue: &thr, MetricValue: &mv,
			Dimensions: map[string]string{"model": "claude-3-5-sonnet"},
			FiredAt:    fired, EmailSent: true,
		},
		{
			ID: 101, TenantID: 7, RuleID: 2, State: "resolved",
			ObservedValue: 1200, FiredAt: fired, ResolvedAt: &resolved, EmailSent: false,
		},
	}
}

func TestAlertEventListSpec(t *testing.T) {
	deps := AlertEventListDeps{
		List: func(_ context.Context, in alerting.ListEventsInput) ([]alerting.AlertEvent, error) {
			if in.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", in.TenantID)
			}
			if in.Limit != alertEventListLimit {
				t.Fatalf("Limit 应为 alertEventListLimit=%d, got %d", alertEventListLimit, in.Limit)
			}
			return fakeAlertEvents(), nil
		},
	}
	spec := AlertEventListSpec(deps)

	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["event_count"].(int) != 2 {
		t.Fatalf("event_count 应 2, got %v", res.Summary["event_count"])
	}
	byState := res.Summary["by_state"].(map[string]any)
	if byState["firing"] != 1 || byState["resolved"] != 1 {
		t.Fatalf("by_state 错: %v", byState)
	}

	items := res.Summary["items"].([]map[string]any)
	e0 := items[0]
	if e0["rule_id"].(int64) != 1 || e0["state"] != "firing" {
		t.Fatalf("event[0] 投影错: %v", e0)
	}
	if e0["observed_value"].(float64) != 0.73 || e0["threshold_value"].(float64) != 0.5 {
		t.Fatalf("event[0] 值投影错: %v", e0)
	}
	if e0["email_sent"] != true {
		t.Fatalf("event[0] email_sent 应 true: %v", e0)
	}
	// Dimensions(同 Filters 来源)暴露。
	dims := e0["dimensions"].(map[string]string)
	if dims["model"] != "claude-3-5-sonnet" {
		t.Fatalf("dimensions 应含 model: %v", dims)
	}
	// firing 事件未解决 → resolved_at=nil;resolved 事件有值。
	if e0["resolved_at"] != nil {
		t.Fatalf("firing 事件 resolved_at 应 nil, got %v", e0["resolved_at"])
	}
	if items[1]["resolved_at"] == nil {
		t.Fatalf("resolved 事件应有 resolved_at")
	}
	// firing 事件无 ThresholdValue 的那条?第二条无 ThresholdValue → nil。
	if items[1]["threshold_value"] != nil {
		t.Fatalf("event[1] 无 ThresholdValue 应 nil, got %v", items[1]["threshold_value"])
	}
	// 不回投 tenant_id。
	if _, has := e0["tenant_id"]; has {
		t.Fatalf("不应投影 tenant_id: %v", e0)
	}
}

func TestAlertEventListStateFilterPassthrough(t *testing.T) {
	var gotState alerting.EventState
	deps := AlertEventListDeps{
		List: func(_ context.Context, in alerting.ListEventsInput) ([]alerting.AlertEvent, error) {
			gotState = in.State
			return nil, nil
		},
	}
	r := req(7)
	r.Args["state"] = "firing"
	if _, err := AlertEventListSpec(deps).Run(context.Background(), r); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotState != "firing" {
		t.Fatalf("state 过滤未透传, got %q", gotState)
	}
}

func TestAlertEventListNilDep(t *testing.T) {
	_, err := AlertEventListSpec(AlertEventListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestAlertEventListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := AlertEventListDeps{
		List: func(_ context.Context, _ alerting.ListEventsInput) ([]alerting.AlertEvent, error) {
			return nil, sentinel
		},
	}
	_, err := AlertEventListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
