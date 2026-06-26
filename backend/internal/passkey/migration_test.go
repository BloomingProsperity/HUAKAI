package passkey

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// collapseWS 把连续空白归一成单个空格, 这样下面的结构性断言
// 能容忍 SQL 列对齐这类美观性的空白 (例如 "JSONB   NOT
// NULL"), 同时仍能检测出 constraint/index/CHECK 被实际删除。
func collapseWS(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
}

func TestMigration0098PasskeyCredentialsShape(t *testing.T) {
	// 杀掉的变异: 移除 tenant 维度的 credential 唯一性或
	// tenant/user 索引, 会导致跨租户 credential 碰撞或 owner 列表变慢。
	up, err := os.ReadFile("../../sql/migrations/0098_passkey_credentials.up.sql")
	if err != nil {
		t.Fatalf("read migration up: %v", err)
	}
	raw := collapseWS(string(up))
	required := []string{
		"CREATE TABLE IF NOT EXISTS passkey_credentials",
		"UNIQUE (tenant_id, credential_id)",
		"CREATE INDEX IF NOT EXISTS idx_passkey_credentials_tenant_user",
		"ON passkey_credentials (tenant_id, user_id)",
		"CREATE TABLE IF NOT EXISTS webauthn_session",
		"session_data JSONB NOT NULL",
		"expires_at TIMESTAMPTZ NOT NULL",
		"CHECK (purpose IN ('register', 'login'))",
	}
	for _, needle := range required {
		if !strings.Contains(raw, collapseWS(needle)) {
			t.Fatalf("migration missing %q", needle)
		}
	}
}

func TestMigration0098RollbackRefusesCredentialData(t *testing.T) {
	// 杀掉的变异: 去掉 rollback guard, 会在仍存在真实 passkey credential 时
	// 允许破坏性的 rollback。
	down, err := os.ReadFile("../../sql/migrations/0098_passkey_credentials.down.sql")
	if err != nil {
		t.Fatalf("read migration down: %v", err)
	}
	raw := collapseWS(string(down))
	if !strings.Contains(raw, collapseWS("IF EXISTS (SELECT 1 FROM passkey_credentials)")) {
		t.Fatalf("down migration must refuse rollback when passkey credentials exist:\n%s", raw)
	}
	if !strings.Contains(raw, "Owner-gated account-security data plan") {
		t.Fatalf("down migration must explain Owner-gated rollback requirement:\n%s", raw)
	}
}
