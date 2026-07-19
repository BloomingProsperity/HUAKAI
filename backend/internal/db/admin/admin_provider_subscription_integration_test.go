//go:build integration_pg

package admin

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminProviderAccountSubscriptionProjectionListDetailAndHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, "subscription-"+suffix)
	t.Cleanup(func() { cleanupAdminProviderSubscriptionGraph(t, context.Background(), pool, tenantID) })
	insertAdminProviderAccountHealthCredential(t, ctx, pool, tenantID, accountID, "codex_cli_oauth", "active", 1, nil, "", "", 0)
	var credentialID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM account_credentials
		WHERE tenant_id=$1 AND provider_account_id=$2 AND auth_mode='codex_cli_oauth'`,
		tenantID, accountID,
	).Scan(&credentialID); err != nil {
		t.Fatalf("读取测试凭据 id: %v", err)
	}
	observedAt := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	var observationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO provider_account_subscription_observations (
			tenant_id, provider_account_id, account_credential_id, credential_version,
			vendor, normalized_plan, raw_plan, scope_kind, source_type, trust_level,
			verification_status, observation_status, mapping_version, observed_at
		) VALUES ($1,$2,$3,1,'openai','pro','pro','personal','oauth_token_response',
			'issuer_response','issuer_response','observed',1,$4)
		RETURNING id`, tenantID, accountID, credentialID, observedAt,
	).Scan(&observationID); err != nil {
		t.Fatalf("写入套餐观测: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_account_subscription_states (
			tenant_id, provider_account_id, current_observation_id,
			vendor, normalized_plan, raw_plan, scope_kind, source_type, trust_level,
			verification_status, state_status, mapping_version,
			first_observed_at, observed_at, changed_at
		) VALUES ($1,$2,$3,'openai','pro','pro','personal','oauth_token_response',
			'issuer_response','issuer_response','observed',1,$4,$4,$4)`,
		tenantID, accountID, observationID, observedAt,
	); err != nil {
		t.Fatalf("写入套餐当前投影: %v", err)
	}

	rows, err := q.ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID: tenantID, LimitCount: 10,
		SubscriptionVendorFilter: "openai", SubscriptionPlanFilter: "pro",
		SubscriptionScopeFilter: "personal", SubscriptionStatusFilter: "observed",
		SubscriptionSourceFilter: "oauth_token_response",
	})
	if err != nil {
		t.Fatalf("ListAdminProviderAccounts: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != accountID {
		t.Fatalf("套餐筛选返回=%+v", rows)
	}
	assertAdminSubscriptionProjection(t, rows[0], observedAt)
	if len(rows[0].Tags) != 0 {
		t.Fatalf("系统套餐标签不得污染人工 tags：%v", rows[0].Tags)
	}

	none, err := q.ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID: tenantID, LimitCount: 10, SubscriptionPlanFilter: "plus",
	})
	if err != nil {
		t.Fatalf("ListAdminProviderAccounts mismatch: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("错误套餐筛选不应返回账号：%+v", none)
	}

	detail, err := q.GetAdminProviderAccount(ctx, GetAdminProviderAccountParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccount: %v", err)
	}
	assertAdminSubscriptionProjection(t, detail, observedAt)

	health, err := q.GetAdminProviderAccountHealth(ctx, GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("GetAdminProviderAccountHealth: %v", err)
	}
	if health.SubscriptionVendor == nil || *health.SubscriptionVendor != "openai" ||
		health.SubscriptionPlan == nil || *health.SubscriptionPlan != "pro" ||
		health.SubscriptionStatus == nil || *health.SubscriptionStatus != "observed" ||
		!health.SubscriptionObservedAt.Valid || !health.SubscriptionObservedAt.Time.Equal(observedAt) {
		t.Fatalf("健康查询没有复用套餐投影：%+v", health)
	}
}

func assertAdminSubscriptionProjection(t *testing.T, row AdminProviderAccountRow, observedAt time.Time) {
	t.Helper()
	if row.SubscriptionVendor == nil || *row.SubscriptionVendor != "openai" ||
		row.SubscriptionPlan == nil || *row.SubscriptionPlan != "pro" ||
		row.SubscriptionRawPlan == nil || *row.SubscriptionRawPlan != "pro" ||
		row.SubscriptionScope == nil || *row.SubscriptionScope != "personal" ||
		row.SubscriptionSource == nil || *row.SubscriptionSource != "oauth_token_response" ||
		row.SubscriptionTrust == nil || *row.SubscriptionTrust != "issuer_response" ||
		row.SubscriptionVerification == nil || *row.SubscriptionVerification != "issuer_response" ||
		row.SubscriptionStatus == nil || *row.SubscriptionStatus != "observed" ||
		row.SubscriptionMappingVersion == nil || *row.SubscriptionMappingVersion != 1 ||
		!row.SubscriptionObservedAt.Valid || !row.SubscriptionObservedAt.Time.Equal(observedAt) {
		t.Fatalf("账号套餐投影不完整：%+v", row)
	}
}

func cleanupAdminProviderSubscriptionGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM provider_account_subscription_states WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `ALTER TABLE provider_account_subscription_observations DISABLE TRIGGER provider_account_subscription_observations_append_only`)
	_, _ = pool.Exec(ctx, `DELETE FROM provider_account_subscription_observations WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `ALTER TABLE provider_account_subscription_observations ENABLE TRIGGER provider_account_subscription_observations_append_only`)
	cleanupAdminProviderAccountHealthGraph(t, ctx, pool, tenantID)
}
