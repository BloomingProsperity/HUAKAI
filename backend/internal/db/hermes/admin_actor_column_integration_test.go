//go:build integration_pg

package hermes

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestHermesOperatorActorColumnsMatchCurrentContract 守住真实管理员归属合同：
// 日志与会话都必须记录操作者来源、ID 和角色，并且不能恢复已退役的模拟用户列。
func TestHermesOperatorActorColumnsMatchCurrentContract(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping hermes admin-actor column integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pg ping: %v", err)
	}

	type columnContract struct {
		name     string
		dataType string
		nullable string
	}
	columns := []columnContract{
		{name: "actor_source", dataType: "text", nullable: "NO"},
		{name: "actor_id", dataType: "bigint", nullable: "NO"},
		{name: "actor_role", dataType: "text", nullable: "YES"},
	}
	retiredColumns := map[string][]string{
		"hermes_audit_events":  {"actor_user_id", "admin_actor_token_id"},
		"hermes_conversations": {"admin_actor_token_id"},
	}

	for table, retired := range retiredColumns {
		for _, want := range columns {
			var dataType, isNullable string
			err := pool.QueryRow(ctx,
				`SELECT data_type, is_nullable
				   FROM information_schema.columns
				  WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
				table, want.name,
			).Scan(&dataType, &isNullable)
			if err != nil {
				t.Fatalf("%s.%s 缺失: %v", table, want.name, err)
			}
			if !strings.EqualFold(dataType, want.dataType) {
				t.Fatalf("%s.%s data_type=%s want %s", table, want.name, dataType, want.dataType)
			}
			if !strings.EqualFold(isNullable, want.nullable) {
				t.Fatalf("%s.%s is_nullable=%s want %s", table, want.name, isNullable, want.nullable)
			}
		}

		for _, column := range retired {
			var count int
			if err := pool.QueryRow(ctx,
				`SELECT count(*)
				   FROM information_schema.columns
				  WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
				table, column,
			).Scan(&count); err != nil {
				t.Fatalf("检查 %s.%s 是否退役: %v", table, column, err)
			}
			if count != 0 {
				t.Fatalf("%s.%s 仍存在，旧模拟身份合同未清理", table, column)
			}
		}
	}
}
