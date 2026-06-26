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
		// 变异:去掉 provider-family 谓词会让两个 provider 协议族都被放行。
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
			// 变异检查:去掉时间窗口会放进旧的高成本行,去掉 DESC 会让消费排序错乱,
			// 去掉 LIMIT 会返回过多行。
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
		// 变异检查:GROUP BY user_id 会把同一用户名下的不同 key 合并,去掉 tenant
		// 谓词会泄漏跨租户的 key,去掉 DESC 会让消费排序错乱,去掉 LIMIT 会返回过多行。
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
			// 变异检查:去掉 FILTER 会把 first-byte 为空的行算进
			// TTFT,去掉 NULLIF 会允许零时长的 TPS 除法,
			// 去掉成功 end_class 过滤会把所有成功都变成错误,
			// 去掉按请求数的 DESC/LIMIT 会让运维面板排序错乱。
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
		// 变异检查:去掉时间窗口会放进旧的高成本行,把 DISTINCT 改成 COUNT 会虚增
		// 活跃用户 / key 数,去掉成功 end_class 过滤会让每个请求都显得成功。
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
		// 变异检查:去掉 UTC 按天分桶会把图表点合并,去掉时间窗口会放进旧用量,
		// 改变排序会破坏概览趋势契约。
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
		// 变异检查:去掉 tenant_id 会泄漏跨租户的告警输入,去掉时间窗口会放进
		// 过时的事件,改变成功分类器会让错误率告警失去判别力。
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
				// 变异检查:对 week/month 用 day 会让同一周的用量返回两个桶;
				// 去掉 tenant/api_key/窗口会扩大调用方的自助查询范围。
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
		// 变异检查:去掉 tenant_id 会泄漏跨租户用量,去掉 api_key_id 会把该用户的
		// 所有 key 聚合在一起,把 from/to 改成必填会破坏全历史汇总契约。
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
		// 变异:去掉 api_key_id 会让一个 key 读到同一用户另一个 key 的请求;
		// 去掉 tenant/user/request 会扩大查询范围。
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
		// 变异:退回到 ORDER BY ... LIMIT 1 会报告单条结算行而非逻辑请求总额,
		// 这些聚合标记就会消失。
		if !strings.Contains(sql, want) {
			t.Fatalf("GetUsageRecordByRequestID SQL missing aggregate marker %q in %q", want, sql)
		}
	}
	if strings.Contains(strings.ToUpper(sql), "LIMIT 1") {
		t.Fatalf("GetUsageRecordByRequestID SQL must not collapse a logical request with LIMIT 1: %q", sql)
	}
}
