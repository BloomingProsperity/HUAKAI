//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestHermes服务主体日志迁移可往返(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_hermes_principal_roundtrip")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(218); err != nil {
		t.Fatalf("迁移到 0218：%v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接临时库：%v", err)
	}
	defer conn.Close(ctx)

	suffix := fmt.Sprint(time.Now().UnixNano())
	var tenantID, userID, apiKeyID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "hermes-roundtrip-"+suffix).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name, status, role, principal_kind)
VALUES ($1, 'Hermes 测试服务主体', 'active', 'user', 'service') RETURNING id`, tenantID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, purpose)
VALUES ($1, $2, 'Hermes 测试 Key', $3, $4, 'active', 'hermes') RETURNING id`, tenantID, userID, "hash-"+suffix, "hk_test_"+suffix).Scan(&apiKeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO hermes_service_principals (tenant_id, user_id, api_key_id) VALUES ($1, $2, $3)`, tenantID, userID, apiKeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO hermes_audit_events (tenant_id, actor_source, actor_id, actor_role, action, result)
VALUES ($1, 'session', 9001, 'platform_admin', 'hermes.chat.start', 'success')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO hermes_tool_calls (tenant_id, actor_source, actor_id, actor_role, tool_name, result_status)
VALUES ($1, 'session', 9001, 'platform_admin', 'log_analyze', 'ok')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO hermes_settings (tenant_id, user_id, enabled, api_source, model_key)
VALUES ($1, $2, FALSE, 'external_openai_compatible', '')`, tenantID, userID); err != nil {
		t.Fatalf("0218 应允许写入外部 OpenAI 兼容来源：%v", err)
	}

	if err := runner.Migrate(211); err != nil {
		t.Fatalf("带 Hermes 日志回退到 0211：%v", err)
	}
	var users, audits, tools int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, userID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM hermes_audit_events WHERE tenant_id=$1`, tenantID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM hermes_tool_calls WHERE tenant_id=$1`, tenantID).Scan(&tools); err != nil {
		t.Fatal(err)
	}
	if users != 0 || audits != 0 || tools != 0 {
		t.Fatalf("回退残留：users=%d audits=%d tools=%d", users, audits, tools)
	}
	if err := runner.Migrate(218); err != nil {
		t.Fatalf("回退后重升到 0218：%v", err)
	}
}
