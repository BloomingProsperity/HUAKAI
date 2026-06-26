//go:build integration_pg

package channelhealth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestChannelHealthSummary_CountsByState(t *testing.T) {
	// 变异:把 GROUP BY 改成按 vendor/account 而非 state,或把该 tenant 的每一行都计入 active;不均衡的 by_state 计数与 total 会变红。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openChannelHealthSummaryPool(t, ctx)
	tenantID := seedChannelHealthSummaryTenant(t, ctx, pool, "counts")
	cleanupChannelHealthSummaryTenants(t, pool, tenantID)
	store := NewPostgresStore(pool)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	olderCooldown := now.Add(15 * time.Minute)
	newerCooldown := now.Add(45 * time.Minute)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "openai", StateActive, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "anthropic", StateActive, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "gemini", StateCoolingDown, &newerCooldown)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "openai", StateDisabled, &olderCooldown)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "anthropic", StateManualPaused, nil)

	summary, err := NewService(store, DefaultPolicy(), nil).SummarizeChannelHealth(ctx, tenantID)
	if err != nil {
		t.Fatalf("SummarizeChannelHealth: %v", err)
	}
	if summary.Total != 5 {
		t.Fatalf("total=%d want 5", summary.Total)
	}
	want := map[HealthState]int64{
		StateActive:       2,
		StateDegraded:     0,
		StateCoolingDown:  1,
		StateRamping:      0,
		StateDisabled:     1,
		StateManualPaused: 1,
	}
	for state, count := range want {
		if summary.ByState[state] != count {
			t.Fatalf("by_state[%s]=%d want %d; all=%+v", state, summary.ByState[state], count, summary.ByState)
		}
	}
	if summary.OldestCooldownAt == nil || !summary.OldestCooldownAt.Equal(olderCooldown) {
		t.Fatalf("oldest_cooldown_at=%v want %s", summary.OldestCooldownAt, olderCooldown.Format(time.RFC3339))
	}
}

func TestChannelHealthSummary_TenantScoped(t *testing.T) {
	// 变异:从聚合查询中去掉 WHERE tenant_id=$1;tenant B 的行会撑大 tenant A 的 total 与各 state 桶。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openChannelHealthSummaryPool(t, ctx)
	tenantA := seedChannelHealthSummaryTenant(t, ctx, pool, "tenant-a")
	tenantB := seedChannelHealthSummaryTenant(t, ctx, pool, "tenant-b")
	cleanupChannelHealthSummaryTenants(t, pool, tenantA, tenantB)
	store := NewPostgresStore(pool)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantA, "openai", StateActive, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantA, "anthropic", StateDisabled, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantB, "openai", StateActive, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantB, "anthropic", StateActive, nil)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantB, "gemini", StateCoolingDown, nil)

	summary, err := NewService(store, DefaultPolicy(), nil).SummarizeChannelHealth(ctx, tenantA)
	if err != nil {
		t.Fatalf("SummarizeChannelHealth: %v", err)
	}
	if summary.Total != 2 {
		t.Fatalf("total=%d want 2; summary=%+v", summary.Total, summary)
	}
	if summary.ByState[StateActive] != 1 || summary.ByState[StateDisabled] != 1 || summary.ByState[StateCoolingDown] != 0 {
		t.Fatalf("tenant-scoped counts mismatch: %+v", summary.ByState)
	}
}

func openChannelHealthSummaryPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedChannelHealthSummaryTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) int64 {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "ch-summary-"+label+"-"+uuid.NewString()).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant %s: %v", label, err)
	}
	return tenantID
}

func seedPostgresChannelHealthRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresStore, tenantID int64, vendor string, state HealthState, cooldownUntil *time.Time) {
	t.Helper()
	suffix := uuid.NewString()
	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, vendor+"-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "ch-summary-pg-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "ch-summary-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var providerAccountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
		 VALUES ($1, $2, $3, $4, 'api_key') RETURNING id`,
		tenantID, providerID, channelID, "ch-summary-account-"+suffix,
	).Scan(&providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	var credentialID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO account_credentials (
		    tenant_id, provider_account_id, vendor, auth_mode, credential_version,
		    encrypted_payload, key_id, nonce, aad_hash
		 ) VALUES ($1, $2, $3, $4, 1, $5, $6, $7, $8)
		 RETURNING id`,
		tenantID, providerAccountID, vendor, channelHealthSummaryAuthMode(vendor), []byte("ciphertext-"+suffix), "test-key", []byte("nonce-"+suffix), "aad-"+suffix,
	).Scan(&credentialID); err != nil {
		t.Fatalf("seed account credential: %v", err)
	}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	_, err := store.UpsertRecord(ctx, Record{
		Key: ChannelKey{
			TenantID:            tenantID,
			Vendor:              vendor,
			ProviderAccountID:   providerAccountID,
			AccountCredentialID: credentialID,
			CredentialVersion:   1,
		},
		State:            state,
		Score:            100,
		ReasonClass:      SignalNone,
		Confidence:       ConfidenceObserved,
		CooldownUntil:    cooldownUntil,
		PolicyVersion:    "channel-health-v1",
		StateEnteredAt:   now.Add(-time.Minute),
		LastTransitionAt: now.Add(-time.Minute),
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("upsert channel health record: %v", err)
	}
}

func channelHealthSummaryAuthMode(vendor string) string {
	if vendor == "gemini" {
		return "aistudio_api_key"
	}
	return "api_key"
}

func cleanupChannelHealthSummaryTenants(t *testing.T, pool *pgxpool.Pool, tenantIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, tenantID := range tenantIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM channel_health_admin_alerts WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM channel_health_audit_events WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM channel_health_state WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
		}
	})
}
