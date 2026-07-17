package alerting

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/alertmetrics"
)

func TestAlertRuleCRUD(t *testing.T) {
	// MUTATION：在 rule 列表/删除中去掉租户过滤，或绕过 comparator/severity 校验；跨租户行会泄漏，或非法规则会被持久化。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))

	if _, err := svc.CreateRule(ctx, CreateRuleInput{
		TenantID:      7,
		Name:          "bad comparator",
		Metric:        "gateway.requests",
		Comparator:    Comparator("eq"),
		Threshold:     100,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad comparator err=%v want ErrInvalidInput", err)
	}
	if _, err := svc.CreateRule(ctx, CreateRuleInput{
		TenantID:      7,
		Name:          "bad severity",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      Severity("emergency"),
		WindowSeconds: 60,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad severity err=%v want ErrInvalidInput", err)
	}

	created, err := svc.CreateRule(ctx, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if created.ID <= 0 || !created.Enabled {
		t.Fatalf("created=%+v want persisted enabled rule", created)
	}

	rules, err := svc.ListRules(ctx, ListRulesInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListRules tenant 7: %v", err)
	}
	if len(rules) != 1 || rules[0].Name != "request spike" {
		t.Fatalf("tenant 7 rules=%+v want created rule", rules)
	}
	otherRules, err := svc.ListRules(ctx, ListRulesInput{TenantID: 8, Limit: 50})
	if err != nil {
		t.Fatalf("ListRules tenant 8: %v", err)
	}
	if len(otherRules) != 0 {
		t.Fatalf("tenant 8 rules=%+v want empty", otherRules)
	}

	updatedName := "request spike disabled"
	updatedThreshold := 250.0
	disabled := false
	updated, err := svc.UpdateRule(ctx, UpdateRuleInput{
		TenantID:  7,
		ID:        created.ID,
		Name:      &updatedName,
		Threshold: &updatedThreshold,
		Enabled:   &disabled,
	})
	if err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if updated.Name != updatedName || updated.Threshold != updatedThreshold || updated.Enabled {
		t.Fatalf("updated=%+v want renamed disabled threshold 250", updated)
	}

	if err := svc.DeleteRule(ctx, 8, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteRule wrong tenant err=%v want ErrNotFound", err)
	}
	if err := svc.DeleteRule(ctx, 7, created.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	rules, err = svc.ListRules(ctx, ListRulesInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListRules after delete: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules after delete=%+v want empty", rules)
	}
}

func TestEvaluateRules_FiresOnBreach(t *testing.T) {
	// MUTATION：把 gte 实现成 gt，或跳过 event 创建；在恰好等于阈值时无法创建本应触发的 firing event。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 50}); err != nil {
		t.Fatalf("EvaluateRules below threshold: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents below threshold: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events below threshold=%+v want none", events)
	}

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 100}); err != nil {
		t.Fatalf("EvaluateRules at threshold: %v", err)
	}
	events, err = svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents firing: %v", err)
	}
	if len(events) != 1 || events[0].State != EventStateFiring || events[0].ObservedValue != 100 {
		t.Fatalf("events=%+v want one firing observed 100", events)
	}
}

func TestEvaluateRules_ResolvesWhenRecovered(t *testing.T) {
	// MUTATION：在恢复时从不把 firing event 标记为 resolved；该 event 一直停在 firing，resolved_at 始终为 nil。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	svc := NewService(NewMemoryStore(), WithClock(clock))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "latency spike",
		Metric:        "gateway.latency_ms",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.latency_ms": 150}); err != nil {
		t.Fatalf("EvaluateRules firing: %v", err)
	}
	now = now.Add(time.Minute)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.latency_ms": 50}); err != nil {
		t.Fatalf("EvaluateRules recovered: %v", err)
	}

	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateResolved, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents resolved: %v", err)
	}
	if len(events) != 1 || events[0].State != EventStateResolved || events[0].ResolvedAt == nil {
		t.Fatalf("events=%+v want one resolved event with resolved_at", events)
	}
}

func TestSilencedAlertNotDelivered(t *testing.T) {
	// MUTATION：投递时忽略与规则匹配的生效中 silence；被静默的 firing 边沿仍会调用 deliverer。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	rule := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	if _, err := svc.CreateSilence(ctx, CreateSilenceInput{
		TenantID: 7,
		RuleID:   &rule.ID,
		Reason:   "maintenance",
		StartsAt: now.Add(-time.Minute),
		EndsAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateSilence: %v", err)
	}

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules silenced breach: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].State != EventStateFiring {
		t.Fatalf("events=%+v want one persisted firing event", events)
	}
	if got := deliverer.Count(); got != 0 {
		t.Fatalf("deliveries=%d want 0 for active silence", got)
	}
}

func TestEvaluateRules_Idempotent(t *testing.T) {
	// MUTATION：每次评估都插入一条新的 firing 行；两次相同的越界评估会产生重复的 firing event。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	for i := 0; i < 2; i++ {
		if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
			t.Fatalf("EvaluateRules breach #%d: %v", i+1, err)
		}
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("firing events=%+v want exactly one", events)
	}
}

func TestSustainedMinutes(t *testing.T) {
	// MUTATION：忽略 sustained_seconds，在首次越界就触发；60s 的越界窗口会过早创建 firing event。
	ctx := context.Background()
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	now := base
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:         7,
		Name:             "sustained cpu",
		Metric:           "cpu_usage_percent",
		MetricType:       MetricTypeCPUUsagePercent,
		Comparator:       ComparatorGTE,
		Threshold:        80,
		Severity:         SeverityCritical,
		WindowSeconds:    60,
		SustainedSeconds: 120,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"cpu_usage_percent": 95}); err != nil {
		t.Fatalf("EvaluateRules initial breach: %v", err)
	}
	now = base.Add(60 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"cpu_usage_percent": 96}); err != nil {
		t.Fatalf("EvaluateRules 60s breach: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents before sustained window: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events before sustained window=%+v want none", events)
	}

	now = base.Add(120 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"cpu_usage_percent": 97}); err != nil {
		t.Fatalf("EvaluateRules sustained breach: %v", err)
	}
	events, err = svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents sustained firing: %v", err)
	}
	if len(events) != 1 || events[0].MetricValue == nil || *events[0].MetricValue != 97 {
		t.Fatalf("events after sustained window=%+v want one firing with latest metric value 97", events)
	}

	now = base
	guardSvc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, guardSvc, CreateRuleInput{
		TenantID:      7,
		Name:          "default immediate",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	})
	if err := guardSvc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules default immediate: %v", err)
	}
	guardEvents, err := guardSvc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents default immediate: %v", err)
	}
	if len(guardEvents) != 1 {
		t.Fatalf("default sustained=0 events=%+v want immediate firing", guardEvents)
	}
}

func TestCooldownSuppression(t *testing.T) {
	// MUTATION：在一个 resolved event 之后忽略 cooldown_seconds；cooldown 期内的第二次越界会创建重复的 firing event。
	ctx := context.Background()
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	now := base
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:        7,
		Name:            "cooldown requests",
		Metric:          "gateway.requests",
		Comparator:      ComparatorGTE,
		Threshold:       100,
		Severity:        SeverityCritical,
		WindowSeconds:   60,
		CooldownSeconds: 300,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules first firing: %v", err)
	}
	now = base.Add(10 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 50}); err != nil {
		t.Fatalf("EvaluateRules recovery: %v", err)
	}
	now = base.Add(100 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 175}); err != nil {
		t.Fatalf("EvaluateRules cooldown breach: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents inside cooldown: %v", err)
	}
	if len(events) != 1 || events[0].State != EventStateResolved {
		t.Fatalf("events inside cooldown=%+v want only resolved first event", events)
	}

	now = base.Add(301 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 180}); err != nil {
		t.Fatalf("EvaluateRules after cooldown: %v", err)
	}
	events, err = svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents after cooldown: %v", err)
	}
	if len(events) != 1 || events[0].MetricValue == nil || *events[0].MetricValue != 180 {
		t.Fatalf("events after cooldown=%+v want one new firing observed 180", events)
	}
}

func TestEventThresholdDimensions(t *testing.T) {
	// MUTATION：创建 firing event 时省略 threshold_value、metric_value 或 dimensions；该 event 就无法说明它为何触发。
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "model scoped usage",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"model": "x"},
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"usage.request_count": 95}); err != nil {
		t.Fatalf("EvaluateRules scoped breach: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents scoped breach: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v want one firing", events)
	}
	event := events[0]
	if event.ThresholdValue == nil || *event.ThresholdValue != 80 {
		t.Fatalf("threshold_value=%v want 80", event.ThresholdValue)
	}
	if event.MetricValue == nil || *event.MetricValue != 95 {
		t.Fatalf("metric_value=%v want 95", event.MetricValue)
	}
	if event.Dimensions["model"] != "x" {
		t.Fatalf("dimensions=%+v want model=x", event.Dimensions)
	}
}

func TestEvaluateRulesFromSourcePassesFilters(t *testing.T) {
	// MUTATION：总是调用全局 metric source 快照并忽略规则过滤条件；受限作用域的 source 永远收不到 model=x。
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	source := &scopedMetricSourceStub{
		global: map[string]float64{"usage.request_count": 50},
		scoped: map[string]float64{
			"model=x": 95,
		},
	}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "model x usage",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"model": "x"},
	})

	if err := svc.EvaluateRulesFromSource(ctx, 7, source); err != nil {
		t.Fatalf("EvaluateRulesFromSource: %v", err)
	}
	if got := source.ScopedCalls(); len(got) != 1 || got[0]["model"] != "x" {
		t.Fatalf("scoped calls=%+v want one model=x call", got)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].MetricValue == nil || *events[0].MetricValue != 95 || events[0].Dimensions["model"] != "x" {
		t.Fatalf("events=%+v want scoped firing metric=95 dimensions model=x", events)
	}
}

// 变异：按启动固定窗口取数、或把两个规则窗口错误合并时，rolluper 的两个 since
// 不再分别等于 now-60s 与 now-3600s，本测试变红。
func TestEvaluateRulesFromSourceUsesDistinctRuleWindows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	rolluper := &recordingWindowRolluper{rollup: alertmetrics.RecentUsageRollup{RequestCount: 10}}
	source := alertmetrics.NewCompositeMetricSource(alertmetrics.CompositeMetricSourceConfig{
		UsageRolluper: rolluper,
		RecentWindow:  10 * time.Minute,
		Now:           func() time.Time { return now },
	})
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	for _, input := range []struct {
		name   string
		window int32
	}{
		{name: "一分钟窗口", window: 60},
		{name: "一小时窗口", window: 3600},
	} {
		mustCreateRule(t, svc, CreateRuleInput{
			TenantID:      7,
			Name:          input.name,
			Metric:        alertmetrics.MetricUsageRequestCount,
			Comparator:    ComparatorGTE,
			Threshold:     1000,
			Severity:      SeverityWarning,
			WindowSeconds: input.window,
		})
	}

	if err := svc.EvaluateRulesFromSource(ctx, 7, source); err != nil {
		t.Fatalf("EvaluateRulesFromSource() error = %v", err)
	}
	if len(rolluper.since) != 2 {
		t.Fatalf("rolluper 调用=%d，want 每个不同窗口各一次", len(rolluper.since))
	}
	sort.Slice(rolluper.since, func(i, j int) bool { return rolluper.since[i].Before(rolluper.since[j]) })
	want := []time.Time{now.Add(-time.Hour), now.Add(-time.Minute)}
	for i := range want {
		if !rolluper.since[i].Equal(want[i]) {
			t.Fatalf("since[%d]=%s，want %s；all=%v", i, rolluper.since[i], want[i], rolluper.since)
		}
	}
}

// 变异：去掉按窗口缓存后，两条同窗口规则各查一次 rollup，调用数从 1 变 2。
func TestEvaluateRulesFromSourceSharesSameWindowSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	rolluper := &recordingWindowRolluper{rollup: alertmetrics.RecentUsageRollup{RequestCount: 10}}
	source := alertmetrics.NewCompositeMetricSource(alertmetrics.CompositeMetricSourceConfig{
		UsageRolluper: rolluper,
		Now:           func() time.Time { return now },
	})
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	for _, name := range []string{"同窗口甲", "同窗口乙"} {
		mustCreateRule(t, svc, CreateRuleInput{
			TenantID:      7,
			Name:          name,
			Metric:        alertmetrics.MetricUsageRequestCount,
			Comparator:    ComparatorGTE,
			Threshold:     1000,
			Severity:      SeverityWarning,
			WindowSeconds: 60,
		})
	}
	if err := svc.EvaluateRulesFromSource(context.Background(), 7, source); err != nil {
		t.Fatalf("EvaluateRulesFromSource() error = %v", err)
	}
	if len(rolluper.since) != 1 || !rolluper.since[0].Equal(now.Add(-time.Minute)) {
		t.Fatalf("同窗口 rolluper 调用=%v，want 仅 %s", rolluper.since, now.Add(-time.Minute))
	}
}

// 变异：强制断言 WindowedMetricSource 或按规则重复 Snapshot 时，旧源会报错或调用数变 2。
func TestEvaluateRulesFromSourceFallsBackToLegacySnapshot(t *testing.T) {
	source := &legacyMetricSourceStub{snapshot: map[string]float64{"usage.request_count": 10}}
	svc := NewService(NewMemoryStore())
	for i, window := range []int32{60, 3600} {
		mustCreateRule(t, svc, CreateRuleInput{
			TenantID:      7,
			Name:          string(rune('甲' + i)),
			Metric:        "usage.request_count",
			Comparator:    ComparatorGTE,
			Threshold:     1000,
			Severity:      SeverityWarning,
			WindowSeconds: window,
		})
	}
	if err := svc.EvaluateRulesFromSource(context.Background(), 7, source); err != nil {
		t.Fatalf("EvaluateRulesFromSource() error = %v", err)
	}
	if source.calls != 1 {
		t.Fatalf("旧 MetricSource Snapshot 调用=%d，want 1", source.calls)
	}
}

// 变异：删除 24 小时上限后，超大历史扫描规则会被持久化而非返回 ErrInvalidInput。
func TestRuleWindowRejectsAboveMaximum(t *testing.T) {
	svc := NewService(NewMemoryStore())
	if _, err := svc.CreateRule(context.Background(), CreateRuleInput{
		TenantID:      7,
		Name:          "上限窗口",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     1,
		Severity:      SeverityWarning,
		WindowSeconds: int32(MaxRuleWindow / time.Second),
	}); err != nil {
		t.Fatalf("24 小时边界应合法：%v", err)
	}
	_, err := svc.CreateRule(context.Background(), CreateRuleInput{
		TenantID:      7,
		Name:          "超大窗口",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     1,
		Severity:      SeverityWarning,
		WindowSeconds: int32(MaxRuleWindow/time.Second) + 1,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超出上限 err=%v，want ErrInvalidInput", err)
	}
}

func TestSilenceScope(t *testing.T) {
	// MUTATION：忽略 platform/group/region 作用域，把受限作用域的 silence 当作全局；p2 告警的投递会被错误地抑制。
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "platform p1",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"platform": "p1"},
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "platform p2",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"platform": "p2"},
	})
	if _, err := svc.CreateSilence(ctx, CreateSilenceInput{
		TenantID: 7,
		Reason:   "p1 maintenance",
		StartsAt: now.Add(-time.Minute),
		EndsAt:   now.Add(time.Minute),
		Platform: "p1",
	}); err != nil {
		t.Fatalf("CreateSilence: %v", err)
	}

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"usage.request_count": 95}); err != nil {
		t.Fatalf("EvaluateRules scoped silences: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents scoped silences: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%+v want both firing events persisted", events)
	}
	notices := deliverer.Notices()
	if len(notices) != 1 || notices[0].RuleName != "platform p2" {
		t.Fatalf("deliveries=%+v want only platform p2 delivered", notices)
	}
}

func TestManualResolvedEvent(t *testing.T) {
	// MUTATION：管理员的 resolve 操作写成普通 resolved 而非 manual_resolved；运维就无法区分自动恢复和人工关闭。
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "manual resolve",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules firing: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents firing: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v want one firing", events)
	}

	now = now.Add(time.Minute)
	manual, err := svc.ManualResolveEvent(ctx, 7, events[0].ID)
	if err != nil {
		t.Fatalf("ManualResolveEvent: %v", err)
	}
	if manual.State != EventStateManualResolved || manual.ResolvedAt == nil || !manual.ResolvedAt.Equal(now) {
		t.Fatalf("manual event=%+v want manual_resolved at %s", manual, now)
	}
}

func TestFiringDeliveryMarksEmailSent(t *testing.T) {
	// MUTATION：投递了通知却从不持久化 email_sent；运维就无法审计哪次 firing 产生了对外通知。
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "email marker",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
		NotifyEmail:   true,
	})
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules firing: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || !events[0].EmailSent {
		t.Fatalf("events=%+v want email_sent true after successful delivery", events)
	}
}

func TestRootNotifyRateLimitSuppressesRepeatDelivery(t *testing.T) {
	// MUTATION：忽略根级 notify 限流窗口；窗口内的第二次 firing 会两次调用 deliverer。
	ctx := context.Background()
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	now := base
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(
		NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringDeliverer(deliverer),
		WithFiringDeliveryRateLimit(5*time.Minute),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "limited root notify",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules first firing: %v", err)
	}
	now = base.Add(30 * time.Second)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 50}); err != nil {
		t.Fatalf("EvaluateRules recovery: %v", err)
	}
	now = base.Add(time.Minute)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 175}); err != nil {
		t.Fatalf("EvaluateRules repeated firing: %v", err)
	}
	if got := deliverer.Count(); got != 1 {
		t.Fatalf("deliveries=%d want one inside root notify limit window", got)
	}
}

func TestFiringEdgeDeliversOnce(t *testing.T) {
	// MUTATION：在每次评估时都投递，而不是只在新建的 firing 边沿投递；deliverer 的调用次数变成 2。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "latency spike",
		Metric:        "gateway.latency_ms",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.latency_ms": 150}); err != nil {
		t.Fatalf("EvaluateRules first firing: %v", err)
	}
	now = now.Add(time.Minute)
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.latency_ms": 175}); err != nil {
		t.Fatalf("EvaluateRules still firing: %v", err)
	}

	notices := deliverer.Notices()
	if len(notices) != 1 {
		t.Fatalf("deliveries=%d want 1", len(notices))
	}
	notice := notices[0]
	if notice.RuleName != "latency spike" || notice.Metric != "gateway.latency_ms" || notice.Comparator != ComparatorGTE ||
		notice.Threshold != 100 || notice.Severity != SeverityCritical || notice.ObservedValue != 150 || !notice.FiredAt.Equal(time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("notice=%+v want first-edge rule and observed details", notice)
	}
}

func TestNilDelivererSafe(t *testing.T) {
	// MUTATION：在 firing 边沿上假设 deliverer 非 nil；本测试会在到达持久化 event 的断言之前 panic。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules nil deliverer: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("firing events=%+v want one persisted event", events)
	}
}

func TestFiringDeliveryErrorDoesNotFailEvaluation(t *testing.T) {
	// MUTATION：把 deliverer 的错误从 EvaluateRules 返回出去；评估会失败，而不是保住 firing event。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	deliveryErr := errors.New("delivery down")
	deliverer := &recordingFiringDeliverer{err: deliveryErr}
	var recordedErr error
	svc := NewService(
		NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringDeliverer(deliverer),
		WithFiringDeliveryErrorRecorder(func(_ context.Context, _ int64, _ FiringNotice, err error) {
			recordedErr = err
		}),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules propagated delivery error: %v", err)
	}
	if !errors.Is(recordedErr, deliveryErr) {
		t.Fatalf("recordedErr=%v want delivery error", recordedErr)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("firing events=%+v want one persisted event", events)
	}
}

func TestAlert_TenantScoped(t *testing.T) {
	// MUTATION：从 rules/events/silences 中去掉租户过滤；租户 A 会看到租户 B 的行，或租户 A 的 silence 会抑制租户 B 的投递。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	ruleA := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "tenant a spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      8,
		Name:          "tenant b spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	if _, err := svc.CreateSilence(ctx, CreateSilenceInput{
		TenantID: 7,
		RuleID:   &ruleA.ID,
		Reason:   "tenant a maintenance",
		StartsAt: now.Add(-time.Minute),
		EndsAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateSilence tenant A: %v", err)
	}

	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules tenant A: %v", err)
	}
	if err := svc.EvaluateRules(ctx, 8, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules tenant B: %v", err)
	}

	eventsA, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents tenant A: %v", err)
	}
	if len(eventsA) != 1 || eventsA[0].TenantID != 7 {
		t.Fatalf("tenant A events=%+v want one persisted silenced firing", eventsA)
	}
	eventsB, err := svc.ListEvents(ctx, ListEventsInput{TenantID: 8, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents tenant B: %v", err)
	}
	if len(eventsB) != 1 || eventsB[0].TenantID != 8 {
		t.Fatalf("tenant B events=%+v want one tenant B firing", eventsB)
	}
	silencesB, err := svc.ListSilences(ctx, ListSilencesInput{TenantID: 8, Limit: 50})
	if err != nil {
		t.Fatalf("ListSilences tenant B: %v", err)
	}
	if len(silencesB) != 0 {
		t.Fatalf("tenant B silences=%+v want empty", silencesB)
	}
	notices := deliverer.Notices()
	if len(notices) != 1 || notices[0].RuleName != "tenant b spike" {
		t.Fatalf("deliveries=%+v want only tenant B unsilenced firing", notices)
	}
}

func mustCreateRule(t *testing.T, svc *Service, in CreateRuleInput) AlertRule {
	t.Helper()
	rule, err := svc.CreateRule(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateRule %q: %v", in.Name, err)
	}
	return rule
}

type recordingFiringDeliverer struct {
	tenantIDs []int64
	notices   []FiringNotice
	err       error
}

func (d *recordingFiringDeliverer) DeliverFiring(_ context.Context, tenantID int64, notice FiringNotice) error {
	d.tenantIDs = append(d.tenantIDs, tenantID)
	d.notices = append(d.notices, notice)
	return d.err
}

func (d *recordingFiringDeliverer) Count() int {
	return len(d.notices)
}

func (d *recordingFiringDeliverer) Notices() []FiringNotice {
	out := make([]FiringNotice, len(d.notices))
	copy(out, d.notices)
	return out
}

type scopedMetricSourceStub struct {
	global      map[string]float64
	scoped      map[string]float64
	scopedCalls []map[string]string
}

type recordingWindowRolluper struct {
	rollup alertmetrics.RecentUsageRollup
	since  []time.Time
}

func (r *recordingWindowRolluper) RecentUsageRollup(_ context.Context, _ int64, since time.Time) (alertmetrics.RecentUsageRollup, error) {
	r.since = append(r.since, since)
	return r.rollup, nil
}

type legacyMetricSourceStub struct {
	snapshot map[string]float64
	calls    int
}

func (s *legacyMetricSourceStub) Snapshot(context.Context, int64) (map[string]float64, error) {
	s.calls++
	return cloneMetricSnapshot(s.snapshot), nil
}

func (s *scopedMetricSourceStub) Snapshot(_ context.Context, _ int64) (map[string]float64, error) {
	return cloneMetricSnapshot(s.global), nil
}

func (s *scopedMetricSourceStub) SnapshotForDimensions(_ context.Context, _ int64, dimensions map[string]string) (map[string]float64, error) {
	s.scopedCalls = append(s.scopedCalls, normalizeStringMap(dimensions))
	out := map[string]float64{}
	for key, value := range s.scoped {
		if dimensionsKey(dimensions) == key {
			out["usage.request_count"] = value
		}
	}
	return out, nil
}

func (s *scopedMetricSourceStub) ScopedCalls() []map[string]string {
	out := make([]map[string]string, 0, len(s.scopedCalls))
	for _, call := range s.scopedCalls {
		out = append(out, normalizeStringMap(call))
	}
	return out
}

func dimensionsKey(dimensions map[string]string) string {
	if dimensions["model"] != "" {
		return "model=" + dimensions["model"]
	}
	return ""
}
