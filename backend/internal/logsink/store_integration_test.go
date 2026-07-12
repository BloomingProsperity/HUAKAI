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

// 真 PG:批量插入 → 过滤/键集分页 → request_id 精确检索 → 清理。
// 变异:ListRuntimeLogs 丢掉 level/request_id/before_id 任一 WHERE 条件 → 对应断言红。
func TestPostgresStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	pool := openLogsinkIntegrationPool(t, ctx)
	store := NewPostgresStore(pool)
	marker := "logsink-it-" + time.Now().UTC().Format("150405.000000000")

	entries := []Entry{
		{Time: time.Now().UTC(), Level: "warn", Component: marker, Message: "warn-1", Attrs: map[string]any{"n": 1}},
		{Time: time.Now().UTC(), Level: "error", Component: marker, Message: "error-1", RequestID: marker + "-req"},
		{Time: time.Now().UTC(), Level: "warn", Component: marker, Message: "warn-2"},
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
	if len(rows) != 3 || rows[0].Message != "warn-2" || rows[2].Message != "warn-1" {
		t.Fatalf("排序/数量错: %+v", rows)
	}

	// level 过滤。
	rows, err = store.ListRuntimeLogs(ctx, ListParams{Component: marker, Level: "error"})
	if err != nil || len(rows) != 1 || rows[0].Message != "error-1" {
		t.Fatalf("level 过滤错: %v %+v", err, rows)
	}

	// request_id 精确检索。
	rows, err = store.ListRuntimeLogs(ctx, ListParams{RequestID: marker + "-req"})
	if err != nil || len(rows) != 1 || rows[0].RequestID == nil || *rows[0].RequestID != marker+"-req" {
		t.Fatalf("request_id 检索错: %v %+v", err, rows)
	}

	// 键集分页:limit=2 取前两条,再用末行 id 取更旧一页。
	page1, err := store.ListRuntimeLogs(ctx, ListParams{Component: marker, Limit: 2})
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1: %v %+v", err, page1)
	}
	page2, err := store.ListRuntimeLogs(ctx, ListParams{Component: marker, BeforeID: page1[1].ID})
	if err != nil || len(page2) != 1 || page2[0].Message != "warn-1" {
		t.Fatalf("page2 键集分页错: %v %+v", err, page2)
	}

	// attrs JSONB 落库可回读。
	if !strings.Contains(string(page2[0].Attrs), `"n": 1`) && !strings.Contains(string(page2[0].Attrs), `"n":1`) {
		t.Fatalf("attrs 未落库: %s", page2[0].Attrs)
	}

	// 清理:删除全部本测试数据(created_at < 未来时刻),行数≥3。
	deleted, err := store.CleanupRuntimeLogs(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || deleted < 3 {
		t.Fatalf("Cleanup: deleted=%d err=%v", deleted, err)
	}
	rows, err = store.ListRuntimeLogs(ctx, ListParams{Component: marker})
	if err != nil || len(rows) != 0 {
		t.Fatalf("清理后应为空: %v %+v", err, rows)
	}
}
