//go:build integration_pg

package admin

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetAdminProviderAccountHealthJoinsLatestNonRevokedCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})

	oldRefreshAt := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	latestRefreshAt := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	revokedRefreshAt := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "api_key", "active", 10, oldRefreshAt, "old_outcome", "temporary", 1)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "refresh_token", "active", 1, latestRefreshAt, "auth_expired", "invalid_grant", 4)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "codex_cli_oauth", "revoked", 9, revokedRefreshAt, "revoked_should_not_win", "revoked", 99)

	row, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{
		TenantID: tenantID,
		ID:       accountID,
	})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth: %v", err)
	}
	if row.ID != accountID || row.TenantID != tenantID || row.HealthState != "throttled" || row.Enabled {
		t.Fatalf("provider account fields=%+v, want tenant/account throttled disabled snapshot", row)
	}
	if !row.HealthStateUntil.Valid || !row.HealthStateUntil.Time.Equal(time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("health_state_until=%+v want 2026-06-02T12:30:00Z", row.HealthStateUntil)
	}
	if row.LastRefreshOutcome == nil || *row.LastRefreshOutcome != "auth_expired" {
		t.Fatalf("last_refresh_outcome=%v want most recent non-revoked auth_expired", row.LastRefreshOutcome)
	}
	if row.FailureClass == nil || *row.FailureClass != "invalid_grant" || row.FailureCount != 4 {
		t.Fatalf("failure metadata class=%v count=%d want invalid_grant/4", row.FailureClass, row.FailureCount)
	}
	if !row.LastRefreshAt.Valid || !row.LastRefreshAt.Time.Equal(latestRefreshAt) {
		t.Fatalf("last_refresh_at=%+v want latest active credential timestamp", row.LastRefreshAt)
	}

	_, err = q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{
		TenantID: tenantID + 1000000,
		ID:       accountID,
	})
	if err == nil {
		t.Fatalf("cross-tenant lookup returned account_id=%d; want no rows", accountID)
	}
	if err != pgx.ErrNoRows {
		t.Fatalf("cross-tenant lookup err=%v want pgx.ErrNoRows", err)
	}
}

func TestGetAdminProviderAccountHealthFallsBackToProviderAccountRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})

	refreshAt := time.Date(2026, 6, 2, 14, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts
		 SET last_refresh_at = $1, last_refresh_outcome = 'refresh_succeeded'
		 WHERE tenant_id = $2 AND id = $3`,
		refreshAt, tenantID, accountID,
	); err != nil {
		t.Fatalf("seed provider account refresh metadata: %v", err)
	}

	row, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{
		TenantID: tenantID,
		ID:       accountID,
	})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth: %v", err)
	}
	if !row.LastRefreshAt.Valid || !row.LastRefreshAt.Time.Equal(refreshAt) {
		t.Fatalf("last_refresh_at=%+v want provider_accounts fallback timestamp", row.LastRefreshAt)
	}
	if row.LastRefreshOutcome == nil || *row.LastRefreshOutcome != "refresh_succeeded" {
		t.Fatalf("last_refresh_outcome=%v want provider_accounts fallback refresh_succeeded", row.LastRefreshOutcome)
	}
	if row.FailureClass != nil || row.FailureCount != 0 {
		t.Fatalf("failure metadata class=%v count=%d want nil/0 without account_credentials", row.FailureClass, row.FailureCount)
	}
}

func seedAdminProviderAccountHealthGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, accountID int64) {
	t.Helper()
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-health-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'Admin Health Provider', 'openai_chat') RETURNING id`,
		tenantID, "admin-health-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "admin-health-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "admin-health-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, enabled, health_state,
			health_state_until, updated_at
		) VALUES ($1, $2, $3, $4, 'api_key', false, 'throttled', $5, $6) RETURNING id`,
		tenantID, providerID, channelID, "admin-health-account-"+suffix,
		time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC),
		time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC),
	).Scan(&accountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}
	return tenantID, accountID
}

func insertAdminProviderAccountHealthCredential(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	accountID int64,
	authMode string,
	state string,
	version int32,
	lastRefreshAt time.Time,
	outcome string,
	failureClass string,
	failureCount int32,
) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
			encrypted_payload, key_id, nonce, aad_hash, last_refresh_at,
			last_refresh_outcome, failure_class, failure_count
		) VALUES (
			$1, $2, 'openai', $3, $4, $5,
			$6, 'test-key', $7, $8, $9,
			$10, $11, $12
		)`,
		tenantID, accountID, authMode, state, version,
		[]byte("ciphertext"), []byte("nonce-12345678"), "aad-"+strconv.FormatInt(accountID, 10)+"-"+authMode,
		lastRefreshAt, outcome, failureClass, failureCount,
	); err != nil {
		t.Fatalf("insert credential auth_mode=%s version=%d: %v", authMode, version, err)
	}
}

func cleanupAdminProviderAccountHealthGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID)
}
