//go:build integration_pg

// backuphttp.PostgresStore 真 PG 集成测试:证 pg_catalog/schema_migrations 只读查询能跑通、
// 返回 public schema 下的真实基表清单(含已知核心表),且不报错。纯只读、零 seeding、零写入。
package backuphttp

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置;跳过集成测试")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestPostgresManifestListsRealPublicTables(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	store := NewPostgresStore(pool)

	data, err := store.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if len(data.Tables) == 0 {
		t.Fatal("public schema 应有基表,实得空清单(pg_class 查询可能失效)")
	}
	// 必须包含已知核心表(证查询真命中业务 schema,非空跑)。
	want := map[string]bool{"tenants": false, "users": false, "api_keys": false}
	for _, tbl := range data.Tables {
		if _, ok := want[tbl.Name]; ok {
			want[tbl.Name] = true
		}
		if tbl.EstimatedRows < 0 {
			t.Fatalf("行数估算不应为负(GREATEST 兜底失效):%s=%d", tbl.Name, tbl.EstimatedRows)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("manifest 应含核心表 %q,实际未出现", name)
		}
	}
	// schema_version 应被读取(>=0;空 schema_migrations 时为 0,不报错)。
	if data.SchemaVersion < 0 {
		t.Fatalf("schema_version 不应为负:%d", data.SchemaVersion)
	}
}
