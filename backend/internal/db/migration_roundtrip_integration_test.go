//go:build integration_pg

// TestMigrationFullRoundtrip proves every migration has a working,
// ordering-correct rollback.  It requires a DEDICATED disposable database
// (not the shared gate DB) because running all .down.sql files is
// destructive.
//
// Usage:
//
//	# provision a fresh DB first (example -adapt to your environment):
//	createdb huakai_roundtrip
//	export HUAKAI_MIGRATE_ROUNDTRIP_DSN="postgres://huakai:huakai@localhost:5432/huakai_roundtrip?sslmode=disable"
//
//	go test -tags=integration_pg -run TestMigrationFullRoundtrip \
//	    -timeout 300s ./internal/db/
//
// When HUAKAI_MIGRATE_ROUNDTRIP_DSN is unset the test is skipped cleanly
// (mirrors the skip pattern in pgconn_integration_test.go).
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

// migrateBin is the path to the golang-migrate CLI installed on the host.
const migrateBin = "/usr/local/bin/migrate"

// TestMigrationFullRoundtrip applies all .up.sql migrations in order,
// rolls them all back in reverse order (down to version 0), then re-applies
// them.  Each phase must succeed and the final schema_migrations.version
// must equal the highest migration number on disk.
func TestMigrationFullRoundtrip(t *testing.T) {
	roundtripDSN := os.Getenv(roundtripDSNEnv)
	if roundtripDSN == "" {
		t.Skipf("%s not set; skipping migration roundtrip integration test", roundtripDSNEnv)
	}

	migDir := roundtripMigrationsDir(t)
	highest := latestMigrationVersion(t) // defined in pgconn_integration_test.go

	t.Logf("migrations dir : %s", migDir)
	t.Logf("highest version: %d", highest)

	// Phase 1 -apply all migrations up.
	t.Run("phase1_up", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "up")
	})

	// Phase 2 -roll back all migrations down to version 0.
	// golang-migrate "down N" with the total count rolls back everything.
	t.Run("phase2_down_all", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "down", "-all")
	})

	// Phase 3 -re-apply all migrations; the schema must be re-creatable
	// from scratch, proving no .down.sql leaves orphaned state that blocks .up.
	t.Run("phase3_re_up", func(t *testing.T) {
		runMigrateCLI(t, migDir, roundtripDSN, "up")
	})

	// Final assertion: schema_migrations must reflect the highest version
	// and must not be dirty.
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

// runMigrateCLI executes /usr/local/bin/migrate with the given subcommand
// and optional arguments.  A non-zero exit or CLI error fails the test.
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

// roundtripMigrationsDir resolves the sql/migrations directory relative to
// this test source file, independent of the working directory at test time.
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
