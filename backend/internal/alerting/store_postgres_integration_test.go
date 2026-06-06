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
	// MUTATION: drop tenant_id SQL predicates in rule/event/silence queries; tenant B rows leak into tenant A or tenant A silence suppresses tenant B.
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
	if len(eventsA) != 0 {
		t.Fatalf("tenant A events=%+v want none due silence", eventsA)
	}
	eventsB, err := svc.ListEvents(ctx, ListEventsInput{TenantID: tenantB, State: EventStateFiring, Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents tenant B: %v", err)
	}
	if len(eventsB) != 1 || eventsB[0].TenantID != tenantB {
		t.Fatalf("tenant B events=%+v want one tenant B firing", eventsB)
	}
}

func TestMigration0103(t *testing.T) {
	// MUTATION: omit any alerting table, tenant/rule/state index, enum CHECK, composite FK, or down DROP; these schema probes fail.
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
