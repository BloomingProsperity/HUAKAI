//go:build integration_pg

package logretention

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openLogRetentionPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPostgresRetentionDeletesAllAllowlistedTablesAtStrictBoundary(t *testing.T) {
	ctx := context.Background()
	pool := openLogRetentionPool(t, ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	assertGlobalLogMigration(t, ctx, tx)
	createIsolatedLogTables(t, ctx, tx)

	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		t.Fatalf("读取数据库可信时间: %v", err)
	}
	cutoff := databaseNow.AddDate(0, 0, -RetentionDays)
	for _, table := range ordinaryLogTables {
		category := table.fixedCategory
		if category == "" {
			category = "access"
		}
		query := fmt.Sprintf(`INSERT INTO %s (ingested_at, log_category) VALUES ($1,$2),($3,$2)`,
			pgx.Identifier{table.name}.Sanitize())
		if _, err := tx.Exec(ctx, query, cutoff.Add(-time.Second), category, cutoff.Add(time.Second)); err != nil {
			t.Fatalf("写入隔离日志表 %s: %v", table.name, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ops_runtime_logs (ingested_at, log_category) VALUES ($1, 'access')`, cutoff); err != nil {
		t.Fatalf("写入精确边界日志: %v", err)
	}

	manager := newManager(&postgresStore{db: tx}, testOption(databaseNow, ordinaryLogTables, 1, 5))
	result, err := manager.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v; result=%+v", err, result)
	}
	if result.Deleted != int64(len(ordinaryLogTables)) || result.HasMore {
		t.Fatalf("应精确删除每张白名单表一条过期日志: %+v", result)
	}
	for _, table := range ordinaryLogTables {
		query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE ingested_at < $1`,
			pgx.Identifier{table.name}.Sanitize())
		var expired int
		if err := tx.QueryRow(ctx, query, cutoff).Scan(&expired); err != nil || expired != 0 {
			t.Fatalf("%s 过期行=%d err=%v", table.name, expired, err)
		}
	}
	var runtimeRows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ops_runtime_logs`).Scan(&runtimeRows); err != nil || runtimeRows != 2 {
		t.Fatalf("第 30 天边界与近期日志必须保留: rows=%d err=%v", runtimeRows, err)
	}
	for _, table := range durableIsolationTables {
		query := fmt.Sprintf(`SELECT count(*) FROM %s`, pgx.Identifier{table}.Sanitize())
		var count int
		if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("持久事实 %s 不得删除: count=%d err=%v", table, count, err)
		}
	}
}

func assertGlobalLogMigration(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	for _, table := range ordinaryLogTables {
		var count int
		if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema='public'
  AND table_name=$1
  AND column_name IN ('ingested_at', 'log_category')`, table.name).Scan(&count); err != nil {
			t.Fatalf("检查 %s 迁移列: %v", table.name, err)
		}
		if count != 2 {
			t.Fatalf("真实表 %s 缺少全局日志列: count=%d", table.name, count)
		}
	}
}

var durableIsolationTables = []string{
	"payment_audit_log",
	"moderation_violation_events",
	"outbox_events",
	"audit_refund_pending",
}

func createIsolatedLogTables(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	for _, table := range ordinaryLogTables {
		query := fmt.Sprintf(`CREATE TEMP TABLE %s (
    id BIGSERIAL PRIMARY KEY,
    ingested_at TIMESTAMPTZ NOT NULL,
    log_category TEXT NOT NULL
) ON COMMIT DROP`, pgx.Identifier{table.name}.Sanitize())
		if _, err := tx.Exec(ctx, query); err != nil {
			t.Fatalf("创建隔离日志表 %s: %v", table.name, err)
		}
	}
	for _, table := range durableIsolationTables {
		query := fmt.Sprintf(`CREATE TEMP TABLE %s (id BIGSERIAL PRIMARY KEY) ON COMMIT DROP`,
			pgx.Identifier{table}.Sanitize())
		if _, err := tx.Exec(ctx, query); err != nil {
			t.Fatalf("创建隔离持久表 %s: %v", table, err)
		}
		insert := fmt.Sprintf(`INSERT INTO %s DEFAULT VALUES`, pgx.Identifier{table}.Sanitize())
		if _, err := tx.Exec(ctx, insert); err != nil {
			t.Fatalf("写入隔离持久表 %s: %v", table, err)
		}
	}
}
