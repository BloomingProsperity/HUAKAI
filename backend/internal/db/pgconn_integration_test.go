//go:build integration_pg

// 真实 PostgreSQL 连接冒烟测试。需要先执行：
//
//	make db-up && make db-migrate
//
// 然后：
//
//	make test-integration
//
// 默认测试套件中会被跳过（无 //go:build => 标准 build tag）。
package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("HUAKAI_DATABASE_URL")
	if v == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestPgConnect 验证 pgxpool 工厂能开启一个真实连接、通过存活探测，
// 并返回一个可用的 *Queries handle。
func TestPgConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	row := pool.QueryRow(ctx, "SELECT 1")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("SELECT 1 scan: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 returned %d, want 1", n)
	}
}

// TestPgSchemaApplied 确认 migration 已落地，且关键钱路径表
// （claim ledger + usage records + outbox）及当前切片要求的结构存在。
// 失败时请运行 `make db-migrate`。
func TestPgSchemaApplied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	expected := []string{
		"billing_ledger_claims",
		"usage_records",
		"scheduler_outbox",
		"provider_accounts",
		"pool_slot_acquisitions",
		"oauth_refresh_audit_events",
		"sticky_bindings",
		"settlement_intents",
		// 破坏点→0185 未应用或漏建 allocation 表，此项存在性探测会转红。
		"tenant_provider_account_allocations",
	}
	for _, table := range expected {
		var present bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)",
			table,
		).Scan(&present)
		if err != nil {
			t.Fatalf("query existence of %s: %v", table, err)
		}
		if !present {
			t.Errorf("expected table %s in schema; not found (did you run `make db-migrate`?)", table)
		}
	}

	// schema_migrations 的合理性检查：已应用的 version 必须匹配磁盘上最高的
	// migration 文件。该值从 sql/migrations 推导，所以一旦新增 migration 而未
	// 重跑 `make db-migrate`，本断言就会自动失败（没有会漂移的硬编码 version）。
	want := latestMigrationVersion(t)
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("schema_migrations probe: %v", err)
	}
	if dirty {
		t.Fatalf("schema_migrations is DIRTY at version %d; previous migration failed mid-flight", version)
	}
	if version != want {
		t.Fatalf("schema_migrations version=%d, expected %d (latest on-disk migration); run `make db-migrate`", version, want)
	}
}

// TestMigration0185Embedded 守住进程内自迁移使用的 go:embed 输入。破坏点→
// 新增 0185 文件后缩窄 embed 规则、漏掉 up 或 down，读取对应路径会失败并转红。
func TestMigration0185Embedded(t *testing.T) {
	paths := []string{
		"migrations/0185_reseller_phase1_tenant_hierarchy.up.sql",
		"migrations/0185_reseller_phase1_tenant_hierarchy.down.sql",
	}
	for _, path := range paths {
		raw, err := sqlmigrations.Files.ReadFile(path)
		if err != nil {
			t.Fatalf("内嵌 migration %s: %v", path, err)
		}
		if len(raw) == 0 {
			t.Fatalf("内嵌 migration %s 为空", path)
		}
	}
}

// latestMigrationVersion 返回 sql/migrations 下 *.up.sql 文件中最高的数字前缀。
// 该目录相对于本测试源文件定位，所以结果与工作目录无关。
func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate migrations dir")
	}
	// thisFile = <repo>/backend/internal/db/pgconn_integration_test.go
	migDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "sql", "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", migDir, err)
	}
	re := regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)
	var versions []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		versions = append(versions, n)
	}
	if len(versions) == 0 {
		t.Fatalf("no *.up.sql migrations found in %s", migDir)
	}
	sort.Ints(versions)
	return versions[len(versions)-1]
}

// TestOpenWithoutDSN 证明 plan §F 的契约：「若某函数无法连到 PG，
// 它返回一个有类型的 error，而不是 200 OK。」
func TestOpenWithoutDSN(t *testing.T) {
	_, err := Open(context.Background(), PoolConfig{DSN: ""})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured for empty DSN; got %v", err)
	}
}
