package alerting

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAlertRuleCRUD(t *testing.T) {
	// MUTATION: drop tenant filtering in rule list/delete or bypass comparator/severity validation; cross-tenant rows leak or invalid rules persist.
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
	// MUTATION: implement gte as gt or skip event creation; equality at threshold fails to create the required firing event.
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
	// MUTATION: never mark firing events resolved on recovery; the event remains firing and resolved_at stays nil.
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
	// MUTATION: ignore active rule-matching silences for delivery; the silenced firing edge calls the deliverer.
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
	// MUTATION: insert a new firing row on every evaluation; two identical breach evaluations produce duplicate firing events.
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
	// MUTATION: ignore sustained_seconds and fire on the first breach; the 60s breach window creates a firing event too early.
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
	// MUTATION: ignore cooldown_seconds after a resolved event; a second breach inside the cooldown creates a duplicate firing event.
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
	// MUTATION: omit threshold_value, metric_value, or dimensions when creating a firing event; the event no longer explains why it fired.
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
	// MUTATION: always call the global metric source snapshot and ignore rule filters; the scoped source never receives model=x.
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

func TestSilenceScope(t *testing.T) {
	// MUTATION: ignore platform/group/region scope and treat a scoped silence as global; the p2 alert delivery is incorrectly suppressed.
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
	// MUTATION: resolve admin action writes regular resolved instead of manual_resolved; operators cannot distinguish recovery from manual closure.
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
	// MUTATION: deliver the notification but never persist email_sent; operators cannot audit which firing produced outbound notification.
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
	// MUTATION: ignore the root notify rate-limit window; a second firing inside the window calls the deliverer twice.
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
	// MUTATION: deliver on every evaluation instead of only a newly created firing edge; the deliverer call count becomes 2.
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
	// MUTATION: assume a non-nil deliverer on the firing edge; this test panics before the persisted event assertion.
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
	// MUTATION: return the deliverer error from EvaluateRules; the evaluation fails instead of preserving the firing event.
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
	// MUTATION: drop tenant filters from rules/events/silences; tenant A sees tenant B rows or tenant A silence suppresses tenant B delivery.
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
