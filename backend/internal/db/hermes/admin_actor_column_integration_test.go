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

// TestHermesAdminActorColumnsExistAndNullable 守 migration 0144:可空的
// admin_actor_token_id 列必须同时存在于 hermes_audit_events 和
// hermes_conversations 上，且不能是 NOT NULL。
//
// 这是有判别力的回归守卫。单元测试用的是内存 fake / sqlc 参数，不反映真实
// schema，因此抓不出缺失或约束错误的列。本测试针对真实 gate DB 运行，若 0144
// 被回退（列不存在），或将来某次改动把列改成 NOT NULL（会破坏既有终端用户路径
// 上从不设置该列的 INSERT），都会失败。已验证其判别力:在 0144 之前，目录查询
// 返回的行为空。
func TestHermesAdminActorColumnsExistAndNullable(t *testing.T) {
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

	for _, table := range []string{"hermes_audit_events", "hermes_conversations"} {
		var dataType, isNullable string
		err := pool.QueryRow(ctx,
			`SELECT data_type, is_nullable
               FROM information_schema.columns
              WHERE table_name = $1 AND column_name = 'admin_actor_token_id'`,
			table,
		).Scan(&dataType, &isNullable)
		if err != nil {
			t.Fatalf("%s.admin_actor_token_id missing (migration 0144 not applied?): %v", table, err)
		}
		if !strings.EqualFold(dataType, "bigint") {
			t.Fatalf("%s.admin_actor_token_id data_type=%s want bigint", table, dataType)
		}
		// 判别要点:该列必须保持可空，这样从不设置它的旧版终端用户
		// INSERT 才能继续成功。
		if !strings.EqualFold(isNullable, "YES") {
			t.Fatalf("%s.admin_actor_token_id is_nullable=%s want YES", table, isNullable)
		}
	}
}
