//go:build integration_pg

// TestMigrationFullRoundtrip 证明每个 migration 都有一个可用且顺序正确的回滚。
// 它需要一个专用的一次性数据库（而非共享的 gate DB），因为跑完所有 .down.sql
// 文件是破坏性操作。
//
// 用法：
//
//	# 先准备一个干净的 DB（示例 —— 按你的环境调整）：
//	createdb huakai_roundtrip
//	export HUAKAI_MIGRATE_ROUNDTRIP_DSN="postgres://huakai:huakai@localhost:5432/huakai_roundtrip?sslmode=disable"
//
//	go test -tags=integration_pg -run TestMigrationFullRoundtrip \
//	    -timeout 300s ./internal/db/
//
// 未设置 HUAKAI_MIGRATE_ROUNDTRIP_DSN 时本测试会被干净地跳过
//（与 pgconn_integration_test.go 中的跳过模式一致）。
package db

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const roundtripDSNEnv = "HUAKAI_MIGRATE_ROUNDTRIP_DSN"

// migrateBin 是宿主机上安装的 golang-migrate CLI 的路径。
const migrateBin = "/usr/local/bin/migrate"

// TestMigrationFullRoundtrip 按顺序应用全部 .up.sql migration，再按逆序全部
// 回滚（降到 version 0），然后重新应用一遍。每个阶段都必须成功，且最终的
// schema_migrations.version 必须等于磁盘上最高的 migration 编号。
func TestMigrationFullRoundtrip(t *testing.T) {
	roundtripDSN := os.Getenv(roundtripDSNEnv)
	if roundtripDSN == "" {
		t.Skipf("%s not set; skipping migration roundtrip integration test", roundtripDSNEnv)
	}

	migDir := roundtripMigrationsDir(t)
	highest := latestMigrationVersion(t) // 定义在 pgconn_integration_test.go

	t.Logf("migrations dir : %s", migDir)
	t.Logf("highest version: %d", highest)

	// 阶段 1 —— 向上应用全部 migration。
	t.Run("phase1_up", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "up")
	})

	// 阶段 2 —— 把全部 migration 回滚到 version 0。
	// golang-migrate 的 "down N"（N 为总数）会回滚所有内容。
	t.Run("phase2_down_all", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "down", "-all")
	})

	// 阶段 3 —— 重新应用全部 migration；schema 必须能从零重建，
	// 证明没有任何 .down.sql 残留会阻塞 .up 的孤立状态。
	t.Run("phase3_re_up", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "up")
	})

	// 最终断言：schema_migrations 必须反映最高 version 且不能是 dirty。
	t.Run("final_version_check", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		pool, err := Open(ctx, PoolConfig{DSN: roundtripDSN})
		if err != nil {
			t.Fatalf("Open pool for final version check: %v", err)
		}
		defer pool.Close()

		var version int
		var dirty bool
		if err := pool.QueryRow(ctx,
			"SELECT version, dirty FROM schema_migrations",
		).Scan(&version, &dirty); err != nil {
			t.Fatalf("schema_migrations probe: %v", err)
		}
		if dirty {
			t.Fatalf("schema_migrations is DIRTY at version %d after roundtrip", version)
		}
		if version != highest {
			t.Fatalf("schema_migrations version=%d, want %d after full roundtrip", version, highest)
		}
		t.Logf("schema_migrations version=%d dirty=%v -OK", version, dirty)
	})
}

// runMigrateCLI 用给定的子命令和可选参数执行 /usr/local/bin/migrate。
// 退出码非零或 CLI 报错都会让测试失败。
func runMigrateCLI(t *testing.T, migDir, dsn string, args ...string) {
	t.Helper()

	cmdArgs := []string{
		"-path", migDir,
		"-database", dsn,
		"-verbose",
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command(migrateBin, cmdArgs...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Logf("migrate %v\n%s", args, string(out))
	if err != nil {
		t.Fatalf("migrate %v failed: %v\noutput:\n%s", args, err, string(out))
	}
}

// roundtripMigrationsDir 相对于本测试源文件解析 sql/migrations 目录，
// 不依赖测试运行时的工作目录。
func roundtripMigrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate migrations dir")
	}
	// thisFile = <repo>/backend/internal/db/migration_roundtrip_integration_test.go
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "sql", "migrations")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("migrations dir %s not accessible: %v", dir, err)
	}
	return dir
}
