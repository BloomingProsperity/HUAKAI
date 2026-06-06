//go:build integration_pg

package db

import (
	"context"
	"testing"
	"time"
)

func TestMigration0097(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	var tablePresent bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1 FROM information_schema.tables
	WHERE table_schema='public' AND table_name='daily_checkin'
)`).Scan(&tablePresent); err != nil {
		t.Fatalf("daily_checkin table probe: %v", err)
	}
	if !tablePresent {
		t.Fatal("daily_checkin table missing")
	}

	var uniquePresent bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM pg_constraint c
	JOIN pg_class rel ON rel.oid = c.conrelid
	WHERE rel.relname='daily_checkin'
	  AND c.conname='uq_daily_checkin_tenant_user_date'
	  AND c.contype='u'
)`).Scan(&uniquePresent); err != nil {
		t.Fatalf("daily_checkin unique probe: %v", err)
	}
	if !uniquePresent {
		t.Fatal("daily_checkin unique constraint uq_daily_checkin_tenant_user_date missing")
	}

	var indexPresent bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname='public'
	  AND tablename='daily_checkin'
	  AND indexname='idx_daily_checkin_tenant_user_date'
)`).Scan(&indexPresent); err != nil {
		t.Fatalf("daily_checkin index probe: %v", err)
	}
	if !indexPresent {
		t.Fatal("daily_checkin index idx_daily_checkin_tenant_user_date missing")
	}
}
