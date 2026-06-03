package billing

import (
	"strings"
	"testing"
)

func TestListEligibleAccountsByPoolGroupSQLFiltersChannelLifecycle(t *testing.T) {
	sql := strings.Join(strings.Fields(listEligibleAccountsByPoolGroup), " ")
	for _, want := range []string{
		"INNER JOIN channels c ON c.id = pa.channel_id",
		"c.enabled = true",
		"c.deleted_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("ListEligibleAccountsByPoolGroup SQL missing channel lifecycle filter %q in %q", want, sql)
		}
	}
}

func TestListEligibleAccountsByPoolGroupSQLFiltersProviderProtocolFamily(t *testing.T) {
	sql := strings.Join(strings.Fields(listEligibleAccountsByPoolGroup), " ")
	for _, want := range []string{
		"INNER JOIN providers p ON p.id = pa.provider_id",
		"p.tenant_id = pa.tenant_id",
		"p.deleted_at IS NULL",
		"p.upstream_protocol = $4",
	} {
		// Mutation: dropping the provider-family predicate lets both provider families through.
		if !strings.Contains(sql, want) {
			t.Fatalf("ListEligibleAccountsByPoolGroup SQL missing provider protocol filter %q in %q", want, sql)
		}
	}
}

func TestBillingSettingsSQLTenantScoped(t *testing.T) {
	for name, sqlText := range map[string]string{
		"get":        getBillingSetting,
		"get_update": getBillingSettingForUpdate,
		"list":       listBillingSettingsByTenant,
		"upsert":     upsertBillingSetting,
	} {
		sql := strings.Join(strings.Fields(sqlText), " ")
		if !strings.Contains(sql, "tenant_id") {
			t.Fatalf("%s billing setting SQL must include tenant scope: %s", name, sql)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(getBillingSetting), " "), "WHERE tenant_id = $1 AND setting_key = $2") {
		t.Fatalf("GetBillingSetting must read by tenant_id and setting_key: %s", getBillingSetting)
	}
	if !strings.Contains(strings.Join(strings.Fields(getBillingSettingForUpdate), " "), "WHERE tenant_id = $1 AND setting_key = $2 FOR UPDATE") {
		t.Fatalf("GetBillingSettingForUpdate must lock by tenant_id and setting_key: %s", getBillingSettingForUpdate)
	}
	lockSQL := strings.Join(strings.Fields(acquireBillingSettingLock), " ")
	if !strings.Contains(lockSQL, "pg_advisory_xact_lock(hashtextextended($1::text, $2::bigint))") {
		t.Fatalf("AcquireBillingSettingLock must use stable tenant/key advisory lock: %s", acquireBillingSettingLock)
	}
	if !strings.Contains(strings.Join(strings.Fields(upsertBillingSetting), " "), "ON CONFLICT (tenant_id, setting_key)") {
		t.Fatalf("UpsertBillingSetting must conflict on tenant_id and setting_key: %s", upsertBillingSetting)
	}
}

func TestUsageLeaderboardSQLUsesWindowSortAndLimit(t *testing.T) {
	for name, sqlText := range map[string]string{
		"user":             aggregateUsageLeaderboardByUser,
		"model":            aggregateUsageLeaderboardByModel,
		"provider_account": aggregateUsageLeaderboardByProviderAccount,
	} {
		sql := strings.Join(strings.Fields(sqlText), " ")
		for _, want := range []string{
			"WHERE ur.settled_at >= $1::timestamptz",
			"ORDER BY sum(ur.actual_cost) DESC",
			"LIMIT $2::int",
		} {
			// Mutation checks: dropping the window admits old high-cost rows,
			// dropping DESC misranks spend, and dropping LIMIT overreturns.
			if !strings.Contains(sql, want) {
				t.Fatalf("%s leaderboard SQL missing %q in %q", name, want, sql)
			}
		}
	}
}
