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

func TestEvaluateRules_SilenceSuppresses(t *testing.T) {
	// MUTATION: ignore active rule-matching silences; the breach creates a firing event despite the silence window.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
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
	if len(events) != 0 {
		t.Fatalf("events=%+v want silence to suppress firing", events)
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

func TestAlert_TenantScoped(t *testing.T) {
	// MUTATION: drop tenant filters from rules/events/silences; tenant A sees tenant B rows or tenant A silence suppresses tenant B firing.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))
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
	if len(eventsA) != 0 {
		t.Fatalf("tenant A events=%+v want none due silence", eventsA)
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
}

func mustCreateRule(t *testing.T, svc *Service, in CreateRuleInput) AlertRule {
	t.Helper()
	rule, err := svc.CreateRule(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateRule %q: %v", in.Name, err)
	}
	return rule
}
