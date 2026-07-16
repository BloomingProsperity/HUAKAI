//go:build integration_pg

// TouchProviderAccountRequestObservedAt 真 PG 验证被动请求观测时间的单调写入、字段分离和租户隔离。
//
// 普通请求完成事件只允许写 last_request_observed_at,不得污染主动探测字段 last_probe_at。
//
// mutation 自检:
//   - 把 UPDATE 的 SET 列改回 last_probe_at → requestObservedAt.Valid 仍为 false且 probeAt 非空 → red。
//   - 把 WHERE 的 id / tenant_id 写错 → 0 行受影响 → after.Valid 仍为 false → red。
//   - 较旧事件晚到 → 不得覆盖较新观测时间。
//   - 跨租户调用(错误 tenant_id)→ 不应写到该账号 → 最后一段断言 red。

package admin

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestTouchProviderAccountRequestObservedAtWritesSeparatedField(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})

	// 前置:两个时间字段都必须是 NULL,否则测不出字段分离。
	var beforeProbe, beforeObserved pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at, last_request_observed_at
			 FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&beforeProbe, &beforeObserved); err != nil {
		t.Fatalf("读取初始观测字段: %v", err)
	}
	if beforeProbe.Valid || beforeObserved.Valid {
		t.Fatalf("种出的账号两个观测字段都应为 NULL, probe=%+v observed=%+v", beforeProbe, beforeObserved)
	}

	observedAt := time.Date(2026, 6, 24, 9, 30, 0, 0, time.UTC)
	if err := q.TouchProviderAccountRequestObservedAt(ctx, TouchProviderAccountRequestObservedAtParams{
		ObservedAt: pgtype.Timestamptz{Time: observedAt, Valid: true},
		ID:         accountID,
		TenantID:   tenantID,
	}); err != nil {
		t.Fatalf("TouchProviderAccountRequestObservedAt: %v", err)
	}

	var afterProbe, afterObserved pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at, last_request_observed_at
			 FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&afterProbe, &afterObserved); err != nil {
		t.Fatalf("读取写后观测字段: %v", err)
	}
	if afterProbe.Valid {
		t.Fatalf("普通请求完成事件污染了主动探测字段: %v", afterProbe.Time)
	}
	if !afterObserved.Valid || !afterObserved.Time.Equal(observedAt) {
		t.Fatalf("last_request_observed_at=%+v want %v", afterObserved, observedAt)
	}

	// 乱序消费或 DLQ 重放较旧事件时,最新观测时间不得倒退。
	olderObservedAt := observedAt.Add(-time.Hour)
	if err := q.TouchProviderAccountRequestObservedAt(ctx, TouchProviderAccountRequestObservedAtParams{
		ObservedAt: pgtype.Timestamptz{Time: olderObservedAt, Valid: true},
		ID:         accountID,
		TenantID:   tenantID,
	}); err != nil {
		t.Fatalf("写入较旧观测时间: %v", err)
	}
	var afterOlder pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_request_observed_at FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&afterOlder); err != nil {
		t.Fatalf("读取较旧事件写入后的 last_request_observed_at: %v", err)
	}
	if !afterOlder.Time.Equal(observedAt) {
		t.Fatalf("较旧事件使观测时间倒退: got %v want unchanged %v", afterOlder.Time, observedAt)
	}

	// 跨租户隔离:用错误 tenant_id 调,不应改动该账号(WHERE tenant_id 守卫)。
	wrongTenant := tenantID + 1_000_000
	if err := q.TouchProviderAccountRequestObservedAt(ctx, TouchProviderAccountRequestObservedAtParams{
		ObservedAt: pgtype.Timestamptz{Time: observedAt.Add(time.Hour), Valid: true},
		ID:         accountID,
		TenantID:   wrongTenant,
	}); err != nil {
		t.Fatalf("跨租户 TouchProviderAccountRequestObservedAt 不应报错(0 行): %v", err)
	}
	var afterCross pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_request_observed_at FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&afterCross); err != nil {
		t.Fatalf("读取跨租户后 last_request_observed_at: %v", err)
	}
	if !afterCross.Time.Equal(observedAt) {
		t.Fatalf("跨租户调用篡改了账号 last_request_observed_at: got %v want unchanged %v", afterCross.Time, observedAt)
	}

	// 软删账号不再接收请求观测更新,覆盖生产 SQL 的 deleted_at IS NULL 守卫。
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET deleted_at = now() WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	); err != nil {
		t.Fatalf("软删账号: %v", err)
	}
	if err := q.TouchProviderAccountRequestObservedAt(ctx, TouchProviderAccountRequestObservedAtParams{
		ObservedAt: pgtype.Timestamptz{Time: observedAt.Add(2 * time.Hour), Valid: true},
		ID:         accountID,
		TenantID:   tenantID,
	}); err != nil {
		t.Fatalf("软删账号写入应返回 0 行而非错误: %v", err)
	}
	var afterDeletedProbe, afterDeletedObserved pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at, last_request_observed_at
		 FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&afterDeletedProbe, &afterDeletedObserved); err != nil {
		t.Fatalf("读取软删后观测字段: %v", err)
	}
	if afterDeletedProbe.Valid {
		t.Fatalf("软删账号主动探测字段被污染: %v", afterDeletedProbe.Time)
	}
	if !afterDeletedObserved.Valid || !afterDeletedObserved.Time.Equal(observedAt) {
		t.Fatalf("软删账号观测时间被更新: got %+v want unchanged %v", afterDeletedObserved, observedAt)
	}
}
