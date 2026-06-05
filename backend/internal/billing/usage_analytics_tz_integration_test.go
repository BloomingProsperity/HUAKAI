//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"
)

// 守 wave-2 P3(usage-analytics 日桶时区): usage_analytics.sql 用
// date_trunc('day', settled_at AT TIME ZONE 'UTC')::timestamptz —— 最后的 ::timestamptz 把
// UTC-truncate 出来的 naive 时间戳按**会话 TimeZone**(非 UTC 服务器)解释 -> 桶标签偏移。
// 正确写法是 ... AT TIME ZONE 'UTC'(按 UTC 解释)。本测试在非 UTC 会话下证明:fixed 习语给
// UTC 日界, buggy ::timestamptz 习语偏移(=bug 真实存在)。
func TestUsageAnalytics_DayBucketIsUTCRegardlessOfSessionTZ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	// 故意把会话时区设成非 UTC(EDT=UTC-4), 以暴露 ::timestamptz 的会话时区依赖。
	if _, err := conn.Exec(ctx, "SET TIME ZONE 'America/New_York'"); err != nil {
		t.Fatalf("set tz: %v", err)
	}
	const settled = "2026-06-04 02:00:00+00" // UTC 日界内, UTC 日起点=2026-06-04T00:00:00Z
	var fixedDay, buggyDay time.Time
	if err := conn.QueryRow(ctx, `
SELECT
  date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS fixed_day,
  date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC')::timestamptz         AS buggy_day`,
		settled,
	).Scan(&fixedDay, &buggyDay); err != nil {
		t.Fatalf("query: %v", err)
	}
	wantUTC := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	if !fixedDay.UTC().Equal(wantUTC) {
		t.Fatalf("fixed day bucket=%s want %s (UTC day start, TZ-independent)", fixedDay.UTC(), wantUTC)
	}
	if buggyDay.UTC().Equal(wantUTC) {
		t.Fatal("buggy ::timestamptz expr unexpectedly matched UTC bucket; non-UTC session not exercised")
	}
}
