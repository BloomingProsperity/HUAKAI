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

func TestUsageLeaderboardByApiKeySQLGroupsAndScopes(t *testing.T) {
	sql := strings.Join(strings.Fields(aggregateUsageLeaderboardByApiKey), " ")
	for _, want := range []string{
		"ur.api_key_id::text AS key",
		"WHERE ur.settled_at >= $1::timestamptz",
		"($2::bigint = 0 OR ur.tenant_id = $2::bigint)",
		"GROUP BY ur.api_key_id",
		"ORDER BY sum(ur.actual_cost) DESC",
		"LIMIT $3::int",
	} {
		// Mutation checks: GROUP BY user_id merges distinct keys owned by the
		// same user, dropping the tenant predicate leaks cross-tenant keys,
		// dropping DESC misranks spend, and dropping LIMIT overreturns.
		if !strings.Contains(sql, want) {
			t.Fatalf("api_key leaderboard SQL missing %q in %q", want, sql)
		}
	}
	if strings.Contains(sql, "GROUP BY ur.user_id") {
		t.Fatalf("api_key leaderboard SQL must not group by user_id: %q", sql)
	}
}

func TestUsagePerformanceSQLUsesSafeLatencyThroughputAndErrorAggregates(t *testing.T) {
	for name, sqlText := range map[string]string{
		"model":            aggregateUsagePerformanceByModel,
		"provider_account": aggregateUsagePerformanceByProviderAccount,
	} {
		sql := strings.Join(strings.Fields(sqlText), " ")
		for _, want := range []string{
			"WHERE ur.settled_at >= $1::timestamptz",
			"avg(EXTRACT(EPOCH FROM (ur.first_byte_at - ur.requested_at)) * 1000) FILTER (WHERE ur.first_byte_at IS NOT NULL)",
			"avg(ur.tokens_output::numeric / NULLIF(EXTRACT(EPOCH FROM (ur.last_event_at - ur.first_byte_at)), 0)) FILTER (WHERE ur.last_event_at IS NOT NULL AND ur.first_byte_at IS NOT NULL AND ur.tokens_output > 0)",
			"count(*) FILTER (WHERE ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))::bigint AS error_count",
			"ORDER BY count(*) DESC",
			"LIMIT $2::int",
		} {
			// Mutation checks: dropping FILTER admits nil first-byte rows into
			// TTFT, dropping NULLIF allows zero-duration TPS division, dropping
			// the success end_class filter turns all successes into errors, and
			// dropping request-count DESC/LIMIT misranks the ops panel.
			if !strings.Contains(sql, want) {
				t.Fatalf("%s performance SQL missing %q in %q", name, want, sql)
			}
		}
	}
}

func TestUsageOverviewSQLUsesWindowDistinctSuccessAndDayBucket(t *testing.T) {
	totalsSQL := strings.Join(strings.Fields(aggregateUsageOverviewTotals), " ")
	for _, want := range []string{
		"WHERE ur.settled_at >= $1::timestamptz",
		"COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text AS total_cost",
		"COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens",
		"count(DISTINCT ur.user_id)::bigint AS active_users",
		"count(DISTINCT ur.api_key_id)::bigint AS active_api_keys",
		"count(*) FILTER (WHERE ur.end_class IN ('stream_end_graceful', 'non_streaming'))::bigint AS success_count",
	} {
		// Mutation checks: dropping the window admits old high-cost rows,
		// changing DISTINCT to COUNT inflates active users/keys, and dropping
		// the success end_class filter makes every request look successful.
		if !strings.Contains(totalsSQL, want) {
			t.Fatalf("overview totals SQL missing %q in %q", want, totalsSQL)
		}
	}

	trendSQL := strings.Join(strings.Fields(aggregateUsageOverviewTrendByDay), " ")
	for _, want := range []string{
		"(date_trunc('day', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day",
		"WHERE ur.settled_at >= $1::timestamptz",
		"GROUP BY 1",
		"ORDER BY 1 ASC",
	} {
		// Mutation checks: dropping UTC day bucketing collapses chart points,
		// dropping the window admits old usage, and changing order destabilizes
		// the overview trend contract.
		if !strings.Contains(trendSQL, want) {
			t.Fatalf("overview trend SQL missing %q in %q", want, trendSQL)
		}
	}
}

func TestRecentUsageRollupByTenantSQLScopesWindowAndOutcome(t *testing.T) {
	sql := strings.Join(strings.Fields(recentUsageRollupByTenant), " ")
	for _, want := range []string{
		"count(*)::bigint AS request_count",
		"count(*) FILTER (WHERE ur.end_class IN ('stream_end_graceful', 'non_streaming'))::bigint AS success_count",
		"count(*) FILTER (WHERE ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))::bigint AS error_count",
		"COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text AS total_cost",
		"WHERE ur.tenant_id = $1::bigint",
		"ur.settled_at >= $2::timestamptz",
	} {
		// Mutation checks: dropping tenant_id leaks cross-tenant alert inputs,
		// dropping the window admits stale incidents, and changing the success
		// classifier makes error-rate alerts non-discriminating.
		if !strings.Contains(sql, want) {
			t.Fatalf("recent tenant rollup SQL missing %q in %q", want, sql)
		}
	}
}

func TestMyUsageTimeSeriesSQLUsesRequestedGranularityAndCallerScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sql    string
		bucket string
	}{
		{name: "day", sql: aggregateMyUsageByDay, bucket: "day"},
		{name: "week", sql: aggregateMyUsageByWeek, bucket: "week"},
		{name: "month", sql: aggregateMyUsageByMonth, bucket: "month"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := strings.Join(strings.Fields(tc.sql), " ")
			for _, want := range []string{
				"(date_trunc('" + tc.bucket + "', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day",
				"ur.tenant_id = $1::bigint",
				"ur.api_key_id = $2::bigint",
				"ur.settled_at >= $3::timestamptz",
				"ur.settled_at < $4::timestamptz",
				"GROUP BY 1, 2",
			} {
				// Mutation checks: using day for week/month returns two buckets
				// for same-week usage; dropping tenant/api_key/window widens the
				// caller's self-serve scope.
				if !strings.Contains(sql, want) {
					t.Fatalf("%s time-series SQL missing %q in %q", tc.name, want, sql)
				}
			}
		})
	}
}

func TestMyUsageTotalsSQLUsesCallerSelectedKeyScope(t *testing.T) {
	sql := strings.Join(strings.Fields(aggregateMyUsageTotals), " ")
	for _, want := range []string{
		"ur.tenant_id = $1::bigint",
		"ur.api_key_id = $2::bigint",
		"($3::timestamptz IS NULL OR ur.settled_at >= $3::timestamptz)",
		"($4::timestamptz IS NULL OR ur.settled_at < $4::timestamptz)",
		"COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text AS total_cost",
		"COALESCE(sum(ur.tokens_input), 0)::bigint AS total_tokens_input",
		"COALESCE(sum(ur.tokens_output), 0)::bigint AS total_tokens_output",
		"COALESCE(sum(ur.cache_read_tokens), 0)::bigint AS total_cache_read_tokens",
		"COALESCE(sum(ur.cache_creation_tokens), 0)::bigint AS total_cache_creation_tokens",
		"count(*)::bigint AS request_count",
	} {
		// Mutation checks: dropping tenant_id leaks cross-tenant usage, dropping
		// api_key_id aggregates all of the user's keys, and making from/to
		// mandatory breaks the full-history summary contract.
		if !strings.Contains(sql, want) {
			t.Fatalf("my usage totals SQL missing %q in %q", want, sql)
		}
	}
	if strings.Contains(sql, "GROUP BY") {
		t.Fatalf("my usage totals must return one totals row, not grouped buckets: %q", sql)
	}
}

func TestGetUsageRecordByRequestIDSQLScopesToCaller(t *testing.T) {
	sql := strings.Join(strings.Fields(getUsageRecordByRequestID), " ")
	for _, want := range []string{
		"ur.tenant_id = $1::bigint",
		"ur.user_id = $2::bigint",
		"ur.api_key_id = $3::bigint",
		"blc.logical_request_id = $4::text",
	} {
		// Mutation: dropping api_key_id lets one key read another key's
		// same-user request; dropping tenant/user/request widens the lookup.
		if !strings.Contains(sql, want) {
			t.Fatalf("GetUsageRecordByRequestID SQL missing caller scope %q in %q", want, sql)
		}
	}
}

func TestGetUsageRecordByRequestIDSQLAggregatesLogicalRequest(t *testing.T) {
	sql := strings.Join(strings.Fields(getUsageRecordByRequestID), " ")
	for _, want := range []string{
		"row_number() OVER",
		"sum(tokens_input)::integer AS tokens_input",
		"sum(tokens_output)::integer AS tokens_output",
		"sum(cache_creation_tokens)::integer AS cache_creation_tokens",
		"sum(cache_read_tokens)::integer AS cache_read_tokens",
		"sum(actual_cost)::numeric AS actual_cost",
	} {
		// Mutation: reverting to ORDER BY ... LIMIT 1 reports one settlement row
		// instead of the logical request total, so these aggregate markers vanish.
		if !strings.Contains(sql, want) {
			t.Fatalf("GetUsageRecordByRequestID SQL missing aggregate marker %q in %q", want, sql)
		}
	}
	if strings.Contains(strings.ToUpper(sql), "LIMIT 1") {
		t.Fatalf("GetUsageRecordByRequestID SQL must not collapse a logical request with LIMIT 1: %q", sql)
	}
}
