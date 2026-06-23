//go:build integration_pg

// dbmigrate 真 PG 验证:进程内自迁移对一个全新空库,必须把 schema 真正建起来并升到最新,
// 且重复运行幂等(无变更不报错)。
//
// 判别 fixture 关键:测试用 HUAKAI_DATABASE_URL 作维护连接、临时 CREATE 一个全新空库,对它跑
// dbmigrate.Up;断言 ① 核心业务表(tenants)在空库里被真正建出来 ② schema_migrations 记录了
// 非零版本且 dirty=false ③ 再跑一次 Up 仍返回 nil(幂等)。把 dbmigrate.Up 改成空操作(不真跑迁移),
// tenants 不存在 → 测试 red;符合 mutation 自检。用后 DROP 临时库,零残留。
package dbmigrate_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
)

func TestUp_AppliesAllMigrationsToEmptyDB_AndIsIdempotent(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()

	// 临时空库名(带纳秒时戳避免并发/重跑撞名)。
	tmpDB := fmt.Sprintf("huakai_dbmigrate_test_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatalf("连接维护库失败: %v", err)
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgxQuoteIdent(tmpDB)); err != nil {
		t.Fatalf("CREATE DATABASE %s 失败: %v", tmpDB, err)
	}
	t.Cleanup(func() {
		// 断开残留连接再 DROP,避免"数据库正被使用"。
		cleanup, cerr := pgx.Connect(context.Background(), baseDSN)
		if cerr != nil {
			t.Logf("清理连接失败(临时库 %s 可能残留): %v", tmpDB, cerr)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(),
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1", tmpDB)
		if _, derr := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgxQuoteIdent(tmpDB)); derr != nil {
			t.Logf("DROP DATABASE %s 失败(需手动清理): %v", tmpDB, derr)
		}
	})

	tmpDSN := swapDatabaseName(t, baseDSN, tmpDB)

	// 首次:空库 → 应用全部迁移。
	if err := dbmigrate.Up(sqlmigrations.Files, tmpDSN); err != nil {
		t.Fatalf("首次 Up(空库)失败: %v", err)
	}

	conn, err := pgx.Connect(ctx, tmpDSN)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer conn.Close(ctx)

	// ① 核心业务表真的建出来了(迁移确实跑了,而非空操作)。
	var tenantsReg *string
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.tenants')::text").Scan(&tenantsReg); err != nil {
		t.Fatalf("查 tenants 表失败: %v", err)
	}
	if tenantsReg == nil {
		t.Fatal("空库跑完 Up 后 public.tenants 仍不存在 —— 迁移没真正应用")
	}

	// ② schema_migrations 记录非零版本且未 dirty。
	var version int64
	var dirty bool
	if err := conn.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("查 schema_migrations 失败(应被 golang-migrate 建出): %v", err)
	}
	if version <= 0 {
		t.Fatalf("schema_migrations.version = %d,应为已应用的最高迁移版本(非零)", version)
	}
	if dirty {
		t.Fatal("schema_migrations.dirty = true —— 迁移中途失败,schema 处于脏态")
	}

	// ③ 再跑一次:无变更,幂等返回 nil(ErrNoChange 被吞)。
	if err := dbmigrate.Up(sqlmigrations.Files, tmpDSN); err != nil {
		t.Fatalf("第二次 Up(已最新)应幂等返回 nil,got %v", err)
	}
}

// swapDatabaseName 把 DSN 里的库名换成 newDB,保留其余连接参数。
func swapDatabaseName(t *testing.T, dsn, newDB string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("解析 DSN 失败: %v", err)
	}
	u.Path = "/" + newDB
	return u.String()
}

// pgxQuoteIdent 给库名加双引号转义,防注入/特殊字符(库名由测试内生成,此处为防御性处理)。
func pgxQuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
