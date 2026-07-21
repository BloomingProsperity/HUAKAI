//go:build integration_pg

package admin

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetAdminProviderAccountHealthSeparatesCurrentCredentialFromRefreshHistory(t *testing.T) {
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
	revokedRefreshAt := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "api_key", "expired", 10, oldRefreshAt, "old_outcome", "temporary", 1)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "refresh_token", "active", 1, nil, "current_never_refreshed", "", 0)
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "codex_cli_oauth", "revoked", 9, revokedRefreshAt, "revoked_should_not_win", "revoked", 99)
	if _, err := pool.Exec(ctx, `
UPDATE account_credentials
SET project_ref = 'project-latest'
WHERE tenant_id = $1 AND provider_account_id = $2 AND auth_mode = 'refresh_token'`, tenantID, accountID); err != nil {
		t.Fatalf("seed latest credential project_ref: %v", err)
	}
	rateReset := time.Date(2026, 6, 2, 12, 40, 0, 0, time.UTC)
	overloadUntil := time.Date(2026, 6, 2, 12, 50, 0, 0, time.UTC)
	tempUntil := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
UPDATE provider_accounts
SET enabled = true,
    credential_state = 'refresh_failed',
    model_rate_limits = '{"model-x":{"rate_limit_reset_at":"2026-06-02T12:45:00Z","reason":"upstream_429"}}'::jsonb,
    rate_limit_reset_at = $1,
    overload_until = $2,
    temp_unschedulable_until = $3
WHERE tenant_id = $4 AND id = $5`,
		rateReset, overloadUntil, tempUntil, tenantID, accountID,
	); err != nil {
		t.Fatalf("seed scheduling snapshot: %v", err)
	}

	row, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{
		TenantID: tenantID,
		ID:       accountID,
	})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth: %v", err)
	}
	if row.ID != accountID || row.TenantID != tenantID || row.HealthState != "throttled" || !row.Enabled {
		t.Fatalf("provider account fields=%+v, want tenant/account throttled enabled snapshot", row)
	}
	if row.ProviderCode != "admin-health-"+suffix || row.AccountType != "api_key" ||
		row.CredentialVendor != "openai" || row.CredentialAuthMode != "refresh_token" ||
		row.CredentialProjectRef == nil || *row.CredentialProjectRef != "project-latest" ||
		row.ServingCredentialCandidates != 1 {
		t.Fatalf("账号观测来源元数据未取当前可服务凭据：%+v", row)
	}
	if !row.HealthStateUntil.Valid || !row.HealthStateUntil.Time.Equal(time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("health_state_until=%+v want 2026-06-02T12:30:00Z", row.HealthStateUntil)
	}
	if row.LastRefreshOutcome == nil || *row.LastRefreshOutcome != "old_outcome" {
		t.Fatalf("last_refresh_outcome=%v want most recent non-revoked refresh history", row.LastRefreshOutcome)
	}
	if row.FailureClass == nil || *row.FailureClass != "temporary" || row.FailureCount != 1 {
		t.Fatalf("failure metadata class=%v count=%d want temporary/1", row.FailureClass, row.FailureCount)
	}
	if !row.LastRefreshAt.Valid || !row.LastRefreshAt.Time.Equal(oldRefreshAt) {
		t.Fatalf("last_refresh_at=%+v want latest refresh history timestamp", row.LastRefreshAt)
	}
	var modelLimits map[string]map[string]string
	if err := json.Unmarshal(row.ModelRateLimits, &modelLimits); err != nil {
		t.Fatalf("decode model_rate_limits: %v raw=%s", err, string(row.ModelRateLimits))
	}
	if row.CredentialState != "refresh_failed" ||
		modelLimits["model-x"]["reason"] != "upstream_429" ||
		modelLimits["model-x"]["rate_limit_reset_at"] != "2026-06-02T12:45:00Z" ||
		row.DisableCooling || !row.ChannelEnabled || !row.ProviderAvailable ||
		!row.RateLimitResetAt.Valid || !row.RateLimitResetAt.Time.Equal(rateReset) ||
		!row.OverloadUntil.Valid || !row.OverloadUntil.Time.Equal(overloadUntil) ||
		!row.TempUnschedulableUntil.Valid || !row.TempUnschedulableUntil.Time.Equal(tempUntil) {
		t.Fatalf("selector/legacy 状态快照未完整回读：%+v", row)
	}

	if _, err := pool.Exec(ctx, `
UPDATE providers
SET enabled = false
WHERE tenant_id = $1
  AND id = (SELECT provider_id FROM provider_accounts WHERE tenant_id = $1 AND id = $2)`, tenantID, accountID); err != nil {
		t.Fatalf("停用供应商：%v", err)
	}
	providerDisabled, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth provider disabled: %v", err)
	}
	if providerDisabled.ProviderAvailable {
		t.Fatalf("停用供应商仍被报告为可用：%+v", providerDisabled)
	}
	if _, err := pool.Exec(ctx, `
UPDATE providers
SET enabled = true
WHERE tenant_id = $1
  AND id = (SELECT provider_id FROM provider_accounts WHERE tenant_id = $1 AND id = $2)`, tenantID, accountID); err != nil {
		t.Fatalf("恢复供应商：%v", err)
	}

	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "codex_cli_oauth", "active", 2, nil, "second_current", "", 0)
	ambiguous, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth ambiguous: %v", err)
	}
	if ambiguous.ServingCredentialCandidates != 2 {
		t.Fatalf("serving_credential_candidates=%d want 2，禁止静默挑选冲突凭据", ambiguous.ServingCredentialCandidates)
	}
	if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET enabled = false WHERE tenant_id = $1 AND id = $2`, tenantID, accountID); err != nil {
		t.Fatalf("disable provider account: %v", err)
	}
	disabled, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth disabled: %v", err)
	}
	if disabled.ServingCredentialCandidates != 0 || disabled.CredentialVendor != "" || disabled.CredentialAuthMode != "" {
		t.Fatalf("停用账号不得报告可服务凭据：%+v", disabled)
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

func TestGetAdminProviderAccountHealthReadsQuotaWindows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})
	start5h := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	end5h := start5h.Add(5 * time.Hour)
	start7d := time.Date(2026, 7, 6, 13, 0, 0, 0, time.UTC)
	end7d := start7d.Add(7 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `
UPDATE provider_accounts
SET session_window_5h_start = $1,
    session_window_5h_end = $2,
    session_window_5h_status = 'active',
    session_window_5h_utilization = 37.50,
    session_window_7d_start = $3,
    session_window_7d_end = $4,
    session_window_7d_status = 'active',
    session_window_7d_utilization = 62.25
WHERE tenant_id = $5 AND id = $6`, start5h, end5h, start7d, end7d, tenantID, accountID); err != nil {
		t.Fatalf("更新窗口快照: %v", err)
	}

	row, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth: %v", err)
	}
	if !row.SessionWindow5hStart.Valid || !row.SessionWindow5hStart.Time.Equal(start5h) ||
		!row.SessionWindow5hEnd.Valid || !row.SessionWindow5hEnd.Time.Equal(end5h) ||
		row.SessionWindow5hStatus == nil || *row.SessionWindow5hStatus != "active" ||
		!row.SessionWindow7dStart.Valid || !row.SessionWindow7dStart.Time.Equal(start7d) ||
		!row.SessionWindow7dEnd.Valid || !row.SessionWindow7dEnd.Time.Equal(end7d) ||
		row.SessionWindow7dStatus == nil || *row.SessionWindow7dStatus != "active" {
		t.Fatalf("窗口时间/状态回读不一致：%+v", row)
	}
	util5h, err := row.SessionWindow5hUtilization.Float64Value()
	if err != nil || !util5h.Valid || util5h.Float64 != 37.5 {
		t.Fatalf("5h utilization=%+v err=%v", util5h, err)
	}
	util7d, err := row.SessionWindow7dUtilization.Float64Value()
	if err != nil || !util7d.Valid || util7d.Float64 != 62.25 {
		t.Fatalf("7d utilization=%+v err=%v", util7d, err)
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
	lastRefreshAt any,
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
