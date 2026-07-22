//go:build integration_pg

package gatewayhttp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// openGatewayHTTPIntegrationPool 为本包所有 PostgreSQL 集成测试建立独立连接。
func openGatewayHTTPIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Fatal("integration_pg 必须设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("打开 PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
