//go:build integration_pg

package alerting

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestPostgresStoreAlertTenantScopeAndEvaluation(t *testing.T) {
	// MUTATION：在 rule/event/silence 查询中去掉 tenant_id 的 SQL 谓词；租户 B 的行会泄漏进租户 A，或租户 A 的 silence 抑制租户 B。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	tenantA := seedAlertingTenant(t, ctx, pool, "alert-a")
	tenantB := seedAlertingTenant(t, ctx, pool, "alert-b")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM alert_silences WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM alert_events WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM alert_rules WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewPostgresStore(pool), WithClock(func() time.Time { return now }))
	ruleA := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantA,
		Name:          "tenant a spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantB,
		Name:          "tenant b spike",
		Metric:        "gateway.requests",
		Comparator:    ComparatorGTE,
		Threshold:     100,
		Severity:      SeverityCritical,
		WindowSeconds: 60,
	})
	if _, err := svc.CreateSilence(ctx, CreateSilenceInput{
		TenantID: tenantA,
		RuleID:   &ruleA.ID,
		Reason:   "tenant a maintenance",
		StartsAt: now.Add(-time.Minute),
		EndsAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateSilence: %v", err)
	}
	if err := svc.EvaluateRules(ctx, tenantA, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules tenant A: %v", err)
	}
	if err := svc.EvaluateRules(ctx, tenantB, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules tenant B: %v", err)
	}
	eventsA, err := svc.ListEvents(ctx, ListEventsInput{TenantID: tenantA, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents tenant A: %v", err)
	}
	if len(eventsA) != 1 || eventsA[0].TenantID != tenantA {
		t.Fatalf("tenant A events=%+v want one persisted silenced firing", eventsA)
	}
	eventsB, err := svc.ListEvents(ctx, ListEventsInput{TenantID: tenantB, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents tenant B: %v", err)
	}
	if len(eventsB) != 1 || eventsB[0].TenantID != tenantB {
		t.Fatalf("tenant B events=%+v want one tenant B firing", eventsB)
	}
}

func TestPostgresStoreAlertEnrichmentRoundTrip(t *testing.T) {
	// MUTATION：在 SQL 中省略 event 的 threshold/metric/dimensions/email_sent 或 rule 的 last_triggered_at；这次往返会丢失告警触发的证据。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	tenantID := seedAlertingTenant(t, ctx, pool, "alert-enrich")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM alert_silences WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM alert_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM alert_rules WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewPostgresStore(pool), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	rule := mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantID,
		Name:          "model x usage",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		NotifyEmail:   true,
		Filters:       map[string]string{"model": "x"},
	})
	if err := svc.EvaluateRules(ctx, tenantID, map[string]float64{"usage.request_count": 95}); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: tenantID, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v want one firing", events)
	}
	event := events[0]
	if event.ThresholdValue == nil || *event.ThresholdValue != 80 ||
		event.MetricValue == nil || *event.MetricValue != 95 ||
		event.Dimensions["model"] != "x" || !event.EmailSent {
		t.Fatalf("event=%+v want threshold 80 metric 95 dimensions model=x email_sent", event)
	}
	storedRule, err := svc.GetRule(ctx, tenantID, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if storedRule.LastTriggeredAt == nil || !storedRule.LastTriggeredAt.Equal(now) {
		t.Fatalf("last_triggered_at=%v want %s", storedRule.LastTriggeredAt, now)
	}
}

func TestPostgresStoreSilenceScope(t *testing.T) {
	// MUTATION：在 SQL/基于 store 的评估中忽略 platform 作用域；platform 级的 p2 告警会被一个只针对 p1 的 silence 抑制。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	tenantID := seedAlertingTenant(t, ctx, pool, "alert-scope")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM alert_silences WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM alert_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM alert_rules WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	deliverer := &recordingFiringDeliverer{}
	svc := NewService(NewPostgresStore(pool), WithClock(func() time.Time { return now }), WithFiringDeliverer(deliverer))
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantID,
		Name:          "platform p1",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"platform": "p1"},
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      tenantID,
		Name:          "platform p2",
		Metric:        "usage.request_count",
		Comparator:    ComparatorGTE,
		Threshold:     80,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Filters:       map[string]string{"platform": "p2"},
	})
	if _, err := svc.CreateSilence(ctx, CreateSilenceInput{
		TenantID: tenantID,
		Reason:   "p1 maintenance",
		StartsAt: now.Add(-time.Minute),
		EndsAt:   now.Add(time.Minute),
		Platform: "p1",
	}); err != nil {
		t.Fatalf("CreateSilence: %v", err)
	}
	if err := svc.EvaluateRules(ctx, tenantID, map[string]float64{"usage.request_count": 95}); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	events, err := svc.ListEvents(ctx, ListEventsInput{TenantID: tenantID, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%+v want both scoped firing events persisted", events)
	}
	notices := deliverer.Notices()
	if len(notices) != 1 || notices[0].RuleName != "platform p2" {
		t.Fatalf("deliveries=%+v want only platform p2 delivered", notices)
	}
}

func TestMigration0103(t *testing.T) {
	// MUTATION：省略任意一个 alerting 表、tenant/rule/state 索引、enum CHECK、复合 FK 或 down DROP；这些 schema 探测会失败。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	defer func() {
		c := context.Background()
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_silences`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_events`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_rules`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS tenants`)
		_, _ = conn.Exec(c, `RESET search_path`)
	}()

	upSQL := readAlertingMigration(t, "0103_alerting.up.sql")
	downSQL := readAlertingMigration(t, "0103_alerting.down.sql")
	if _, err := conn.Exec(ctx, `SET search_path TO pg_temp`); err != nil {
		t.Fatalf("set temp search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE tenants (id bigint PRIMARY KEY)`); err != nil {
		t.Fatalf("create temp tenants: %v", err)
	}
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("0103 up in temp schema: %v", err)
	}

	for _, table := range []string{"alert_rules", "alert_events", "alert_silences"} {
		var count int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = $1`, table).Scan(&count); err != nil {
			t.Fatalf("table probe %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("temp table %s count=%d want 1", table, count)
		}
	}
	var ruleChecks int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE conrelid = 'alert_rules'::regclass
  AND contype = 'c'
  AND pg_get_constraintdef(oid) ILIKE '%comparator%'
  AND pg_get_constraintdef(oid) ILIKE '%gte%'
  AND pg_get_constraintdef(oid) ILIKE '%critical%'`).Scan(&ruleChecks); err != nil {
		t.Fatalf("alert_rules check probe: %v", err)
	}
	if ruleChecks == 0 {
		t.Fatal("alert_rules comparator/severity CHECK missing")
	}
	var eventIndexes int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE schemaname LIKE 'pg_temp_%'
  AND tablename = 'alert_events'
  AND indexdef ILIKE '%tenant_id%'
  AND indexdef ILIKE '%rule_id%'
  AND indexdef ILIKE '%state%'`).Scan(&eventIndexes); err != nil {
		t.Fatalf("alert_events index probe: %v", err)
	}
	if eventIndexes == 0 {
		t.Fatal("alert_events tenant/rule/state index missing")
	}
	var silenceIndexes int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE schemaname LIKE 'pg_temp_%'
  AND tablename = 'alert_silences'
  AND indexdef ILIKE '%tenant_id%'
  AND indexdef ILIKE '%ends_at%'`).Scan(&silenceIndexes); err != nil {
		t.Fatalf("alert_silences index probe: %v", err)
	}
	if silenceIndexes == 0 {
		t.Fatal("alert_silences tenant/ends_at index missing")
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("0103 down in temp schema: %v", err)
	}
	for _, table := range []string{"alert_rules", "alert_events", "alert_silences"} {
		var count int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = $1`, table).Scan(&count); err != nil {
			t.Fatalf("post-down table probe %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("temp table %s after down count=%d want 0", table, count)
		}
	}
}

func TestMigration0114AlertingEnrichment(t *testing.T) {
	// MUTATION：省略 enrichment 列、manual_resolved 状态、默认值或 down 清理；这些 schema 探测会在临时 schema 中失败。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAlertingPool(t, ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	defer func() {
		c := context.Background()
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_silences`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_events`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS alert_rules`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS tenants`)
		_, _ = conn.Exec(c, `RESET search_path`)
	}()

	if _, err := conn.Exec(ctx, `SET search_path TO pg_temp`); err != nil {
		t.Fatalf("set temp search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE tenants (id bigint PRIMARY KEY)`); err != nil {
		t.Fatalf("create temp tenants: %v", err)
	}
	if _, err := conn.Exec(ctx, readAlertingMigration(t, "0103_alerting.up.sql")); err != nil {
		t.Fatalf("0103 up in temp schema: %v", err)
	}
	upSQL := readAlertingMigration(t, "0114_alerting_enrichment.up.sql")
	downSQL := readAlertingMigration(t, "0114_alerting_enrichment.down.sql")
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("0114 up in temp schema: %v", err)
	}

	for _, probe := range []struct {
		table  string
		column string
	}{
		{"alert_rules", "metric_type"},
		{"alert_rules", "sustained_seconds"},
		{"alert_rules", "cooldown_seconds"},
		{"alert_rules", "notify_email"},
		{"alert_rules", "filters"},
		{"alert_rules", "last_triggered_at"},
		{"alert_events", "threshold_value"},
		{"alert_events", "metric_value"},
		{"alert_events", "dimensions"},
		{"alert_events", "email_sent"},
		{"alert_silences", "platform"},
		{"alert_silences", "group_id"},
		{"alert_silences", "region"},
	} {
		var count int
		if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema LIKE 'pg_temp_%'
  AND table_name=$1
  AND column_name=$2`, probe.table, probe.column).Scan(&count); err != nil {
			t.Fatalf("column probe %s.%s: %v", probe.table, probe.column, err)
		}
		if count != 1 {
			t.Fatalf("column %s.%s count=%d want 1", probe.table, probe.column, count)
		}
	}

	if _, err := conn.Exec(ctx, `INSERT INTO tenants (id) VALUES (1)`); err != nil {
		t.Fatalf("insert temp tenant: %v", err)
	}
	var ruleID int64
	if err := conn.QueryRow(ctx, `
INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, severity, window_seconds)
VALUES (1, 'default enrichment', 'gateway.requests', 'gte', 100, 'warning', 60)
RETURNING id`).Scan(&ruleID); err != nil {
		t.Fatalf("insert default rule: %v", err)
	}
	var sustained, cooldown int
	var notifyEmail bool
	if err := conn.QueryRow(ctx, `
SELECT sustained_seconds, cooldown_seconds, notify_email
FROM alert_rules
WHERE id=$1`, ruleID).Scan(&sustained, &cooldown, &notifyEmail); err != nil {
		t.Fatalf("default probe: %v", err)
	}
	if sustained != 0 || cooldown != 0 || notifyEmail {
		t.Fatalf("defaults sustained=%d cooldown=%d notify_email=%v want 0/0/false", sustained, cooldown, notifyEmail)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO alert_events (tenant_id, rule_id, state, observed_value, fired_at, resolved_at)
VALUES (1, $1, 'manual_resolved', 150, now(), now())`, ruleID); err != nil {
		t.Fatalf("manual_resolved insert after 0114: %v", err)
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("0114 down in temp schema: %v", err)
	}
	for _, probe := range []struct {
		table  string
		column string
	}{
		{"alert_rules", "metric_type"},
		{"alert_events", "metric_value"},
		{"alert_silences", "platform"},
	} {
		var count int
		if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema LIKE 'pg_temp_%'
  AND table_name=$1
  AND column_name=$2`, probe.table, probe.column).Scan(&count); err != nil {
			t.Fatalf("post-down column probe %s.%s: %v", probe.table, probe.column, err)
		}
		if count != 0 {
			t.Fatalf("column %s.%s after down count=%d want 0", probe.table, probe.column, count)
		}
	}
}

func openAlertingPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedAlertingTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) int64 {
	t.Helper()
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return id
}

func readAlertingMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "sql", "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
