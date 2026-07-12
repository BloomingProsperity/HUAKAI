//go:build integration_pg

package db

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestMigration0174ModelsProtocolFamilyCheckRoundtrip 在真实 PostgreSQL 上守住
// 0174 的 up/down：up 只扩入 session family；有使用行时 down 必须拒绝；清理后
// down 恢复 0172 完整集合而不是更早的缩水集合。
// 变异：删 up 成员、删 down 数据守卫、或 down 漏掉 gemini_code_assist，均会变红。
func TestMigration0174ModelsProtocolFamilyCheckRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn(t))
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	defer conn.Close(ctx)

	schema := "migration_0174_" + strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "_")
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("创建临时 schema: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	}()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("设置 search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `
CREATE TABLE models (
    protocol_family text NOT NULL,
    CONSTRAINT models_protocol_family_check CHECK (
        protocol_family IN (
            'anthropic_messages', 'openai_chat', 'openai_responses',
            'gemini_messages', 'openrouter_chat', 'bedrock_invoke', 'grok_chat',
            'deepseek_chat', 'mistral_chat', 'groqcloud_chat', 'together_chat',
            'perplexity_chat', 'fireworks_chat', 'openai_codex', 'cursor_session',
            'copilot_session', 'gemini_advanced_session', 'antigravity_session',
            'kiro_session', 'windsurf_session', 'kimi_chat', 'qwen_chat', 'glm_chat',
            'yi_chat', 'baichuan_chat', 'doubao_chat', 'ernie_chat', 'step_chat',
            'hunyuan_chat', 'minimax_chat', 'cohere_chat', 'ollama_chat',
            'ollama_native', 'dify_chat', 'replicate_image', 'vertex_gemini',
            'vertex_anthropic', 'gemini_code_assist'
        )
    )
)`); err != nil {
		t.Fatalf("创建带 0172 CHECK 的 models: %v", err)
	}

	assertProtocolFamilyInsertFails(t, ctx, conn, "anthropic_claude_session")
	if _, err := conn.Exec(ctx, readMigration0174(t, "0174_models_protocol_family_anthropic_claude_session.up.sql")); err != nil {
		t.Fatalf("执行 0174 up: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO models (protocol_family) VALUES ('anthropic_claude_session')"); err != nil {
		t.Fatalf("0174 up 后 session family 应可写入: %v", err)
	}

	if _, err := conn.Exec(ctx, readMigration0174(t, "0174_models_protocol_family_anthropic_claude_session.down.sql")); err == nil {
		t.Fatal("仍有 session family 行时 0174 down 必须拒绝")
	}
	_, _ = conn.Exec(ctx, "ROLLBACK")
	if _, err := conn.Exec(ctx, "DELETE FROM models WHERE protocol_family = 'anthropic_claude_session'"); err != nil {
		t.Fatalf("清理 session family 行: %v", err)
	}
	if _, err := conn.Exec(ctx, readMigration0174(t, "0174_models_protocol_family_anthropic_claude_session.down.sql")); err != nil {
		t.Fatalf("清理后执行 0174 down: %v", err)
	}
	assertProtocolFamilyInsertFails(t, ctx, conn, "anthropic_claude_session")
	if _, err := conn.Exec(ctx, "INSERT INTO models (protocol_family) VALUES ('gemini_code_assist')"); err != nil {
		t.Fatalf("0174 down 必须保留 0172 完整 allowlist: %v", err)
	}
}

func readMigration0174(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 migration %s: %v", name, err)
	}
	return string(raw)
}
