//go:build integration_pg

package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestMigration0172ModelsProtocolFamilyCheckRoundtrip 守 0172 的真实 DDL 行为:
// 旧 CHECK 必须拒绝 hunyuan_chat；up 后必须允许；down 在有新增族数据时必须
// fail-fast；清理新增族数据后 down 必须缩回并再次拒绝 hunyuan_chat。
// 变异证明:把 up 迁移删掉 hunyuan_chat → up 后 INSERT 子断言红；删掉 down
// guard → 有数据回滚子断言红；down 未缩回旧集合 → 最后 INSERT 子断言红。
func TestMigration0172ModelsProtocolFamilyCheckRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer conn.Close(ctx)

	schema := "migration_0172_" + strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "_")
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create temp schema: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	}()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `
CREATE TABLE models (
    protocol_family text NOT NULL,
    CONSTRAINT models_protocol_family_check CHECK (
        protocol_family IN (
            'anthropic_messages',
            'openai_chat',
            'openai_responses',
            'gemini_messages',
            'openrouter_chat',
            'bedrock_invoke',
            'grok_chat',
            'deepseek_chat',
            'mistral_chat',
            'groqcloud_chat',
            'together_chat',
            'perplexity_chat',
            'fireworks_chat',
            'openai_codex',
            'cursor_session',
            'copilot_session',
            'gemini_advanced_session',
            'antigravity_session',
            'kiro_session',
            'windsurf_session'
        )
    )
)`); err != nil {
		t.Fatalf("create models table with old check: %v", err)
	}

	assertProtocolFamilyInsertFails(t, ctx, conn, "hunyuan_chat")

	if _, err := conn.Exec(ctx, readMigration0172(t, "0172_models_protocol_family_registered_adapters.up.sql")); err != nil {
		t.Fatalf("apply 0172 up: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO models (protocol_family) VALUES ('hunyuan_chat')"); err != nil {
		t.Fatalf("hunyuan_chat should insert after 0172 up: %v", err)
	}

	_, err = conn.Exec(ctx, readMigration0172(t, "0172_models_protocol_family_registered_adapters.down.sql"))
	if err == nil {
		t.Fatal("0172 down should refuse while hunyuan_chat rows exist")
	}
	_, _ = conn.Exec(ctx, "ROLLBACK")

	if _, err := conn.Exec(ctx, "DELETE FROM models WHERE protocol_family = 'hunyuan_chat'"); err != nil {
		t.Fatalf("delete new-family rows: %v", err)
	}
	if _, err := conn.Exec(ctx, readMigration0172(t, "0172_models_protocol_family_registered_adapters.down.sql")); err != nil {
		t.Fatalf("apply 0172 down after cleanup: %v", err)
	}
	assertProtocolFamilyInsertFails(t, ctx, conn, "hunyuan_chat")
}

func assertProtocolFamilyInsertFails(t *testing.T, ctx context.Context, conn *pgx.Conn, family string) {
	t.Helper()
	_, err := conn.Exec(ctx, "INSERT INTO models (protocol_family) VALUES ($1)", family)
	if err == nil {
		t.Fatalf("insert protocol_family=%q should fail before CHECK expansion", family)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "models_protocol_family_check" {
		return
	}
	if strings.Contains(err.Error(), "models_protocol_family_check") ||
		(strings.Contains(err.Error(), "violates check constraint") && strings.Contains(err.Error(), family)) {
		return
	}
	t.Fatalf("insert protocol_family=%q failed with unexpected error: %v", family, err)
}

func readMigration0172(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(raw)
}
