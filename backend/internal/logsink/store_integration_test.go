//go:build integration_pg

package logsink

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openLogsinkIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

// 真 PG：批量插入 → 全字段过滤 → 键集分页 → 多关联标识精确检索。
// 变异:ListRuntimeLogs 丢掉 level/request_id/before_id 任一 WHERE 条件 → 对应断言红。
// 全程跑在回滚事务里，避免共享测试库残留观测数据。
func TestPostgresStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	pool := openLogsinkIntegrationPool(t, ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	store := NewPostgresStore(tx)
	marker := "logsink-it-" + time.Now().UTC().Format("150405.000000000")

	tenantID := int64(7)
	entries := []Entry{
		{
			Time: time.Now().UTC().AddDate(-1, 0, 0), Level: "info", Category: "access",
			EventType: "http.request_completed", Result: "success", ErrorClass: "none", ErrorCode: "none",
			Component: marker, Message: "info-1", ActorKind: "api_key", ActorRef: "key-7", TenantID: &tenantID,
			TargetType: "http_route", TargetRef: "/v1/chat", RequestID: marker + "-req",
			TraceID: marker + "-trace", UpstreamRequestID: marker + "-upstream",
			IdempotencyKey: marker + "-idem", RecoveryState: "none", Attrs: map[string]any{"n": 1},
		},
		{Time: time.Now().UTC(), Level: "error", Category: "financial", EventType: "billing.refund_failed", Result: "server_failure", ErrorClass: "dependency", ErrorCode: "billing_store_down", Component: marker, Message: "error-1", RequestID: marker + "-error", Retryable: true, RecoveryState: "retrying"},
		{Time: time.Now().UTC(), Level: "warn", Component: marker, Message: "warn-1"},
	}
	if err := store.InsertRuntimeLogs(ctx, entries); err != nil {
		t.Fatalf("InsertRuntimeLogs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ops_runtime_logs WHERE component = $1`, marker)
	})

	// 全量(按 component 隔离测试数据):id 降序 = 插入逆序。
	rows, err := store.ListRuntimeLogs(ctx, ListParams{Component: marker})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 || rows[0].Message != "warn-1" || rows[2].Message != "info-1" {
		t.Fatalf("排序/数量错: %+v", rows)
	}
	if rows[2].IngestedAt.IsZero() || !rows[2].IngestedAt.After(rows[2].CreatedAt) {
		t.Fatalf("可信入库时间应独立于旧事件时间: %+v", rows[2])
	}

	// level 过滤。
	rows, err = store.ListRuntimeLogs(ctx, ListParams{
		Component: marker, Level: "error", Category: "financial", EventType: "billing.refund_failed",
		Result: "server_failure", ErrorClass: "dependency", ErrorCode: "billing_store_down",
		RecoveryState: "retrying",
	})
	if err != nil || len(rows) != 1 || rows[0].Message != "error-1" {
		t.Fatalf("level 过滤错: %v %+v", err, rows)
	}

	// request_id 精确检索。
	rows, err = store.ListRuntimeLogs(ctx, ListParams{
		RequestID: marker + "-req", TraceID: marker + "-trace", UpstreamRequestID: marker + "-upstream",
		IdempotencyKey: marker + "-idem", ActorKind: "api_key", TenantID: tenantID,
	})
	if err != nil || len(rows) != 1 || rows[0].RequestID == nil || *rows[0].RequestID != marker+"-req" {
		t.Fatalf("request_id 检索错: %v %+v", err, rows)
	}
	if rows[0].TraceID == nil || rows[0].UpstreamRequestID == nil || rows[0].IdempotencyKey == nil ||
		rows[0].TargetType == nil || rows[0].TargetRef == nil || rows[0].ActorRef == nil {
		t.Fatalf("结构化关联字段未完整回读: %+v", rows[0])
	}

	// 键集分页:limit=2 取前两条,再用末行 id 取更旧一页。
	page1, err := store.ListRuntimeLogs(ctx, ListParams{Component: marker, Limit: 2})
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1: %v %+v", err, page1)
	}
	page2, err := store.ListRuntimeLogs(ctx, ListParams{Component: marker, BeforeID: page1[1].ID})
	if err != nil || len(page2) != 1 || page2[0].Message != "info-1" {
		t.Fatalf("page2 键集分页错: %v %+v", err, page2)
	}

	// attrs JSONB 落库可回读。
	if !strings.Contains(string(page2[0].Attrs), `"n": 1`) && !strings.Contains(string(page2[0].Attrs), `"n":1`) {
		t.Fatalf("attrs 未落库: %s", page2[0].Attrs)
	}

}
