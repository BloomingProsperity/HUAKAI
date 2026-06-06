//go:build integration_pg

package db

import (
	"context"
	"testing"
	"time"
)

func TestMigration0100ReferralRewardIssuance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	for _, column := range []struct {
		name     string
		nullable string
	}{
		{name: "receipt_id", nullable: "YES"},
		{name: "billing_event_id", nullable: "YES"},
		{name: "currency_code", nullable: "NO"},
	} {
		var nullable string
		if err := pool.QueryRow(ctx, `
SELECT is_nullable
FROM information_schema.columns
WHERE table_schema='public' AND table_name='referral_rewards' AND column_name=$1`, column.name).Scan(&nullable); err != nil {
			t.Fatalf("referral_rewards.%s column probe: %v", column.name, err)
		}
		if nullable != column.nullable {
			t.Fatalf("referral_rewards.%s nullable=%s want %s", column.name, nullable, column.nullable)
		}
	}

	var uniquePresent bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM pg_constraint c
	JOIN pg_class rel ON rel.oid = c.conrelid
	WHERE rel.relname='referral_rewards'
	  AND c.conname='referral_rewards_tenant_referral_unique'
	  AND c.contype='u'
)`).Scan(&uniquePresent); err != nil {
		t.Fatalf("referral_rewards unique probe: %v", err)
	}
	if !uniquePresent {
		t.Fatal("unique constraint referral_rewards_tenant_referral_unique missing")
	}
}
