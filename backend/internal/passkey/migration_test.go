package passkey

import (
	"os"
	"strings"
	"testing"
)

func TestMigration0098PasskeyCredentialsShape(t *testing.T) {
	// Mutation killed: removing the tenant-scoped credential uniqueness or
	// tenant/user index allows cross-tenant credential collision or slow owner lists.
	up, err := os.ReadFile("../../sql/migrations/0098_passkey_credentials.up.sql")
	if err != nil {
		t.Fatalf("read migration up: %v", err)
	}
	raw := string(up)
	required := []string{
		"CREATE TABLE IF NOT EXISTS passkey_credentials",
		"UNIQUE (tenant_id, credential_id)",
		"CREATE INDEX IF NOT EXISTS idx_passkey_credentials_tenant_user",
		"ON passkey_credentials (tenant_id, user_id)",
		"CREATE TABLE IF NOT EXISTS webauthn_session",
		"session_data JSONB NOT NULL",
		"expires_at   TIMESTAMPTZ NOT NULL",
		"CHECK (purpose IN ('register', 'login'))",
	}
	for _, needle := range required {
		if !strings.Contains(raw, needle) {
			t.Fatalf("migration missing %q", needle)
		}
	}
}

func TestMigration0098RollbackRefusesCredentialData(t *testing.T) {
	// Mutation killed: dropping the rollback guard allows destructive rollback
	// while real passkey credentials still exist.
	down, err := os.ReadFile("../../sql/migrations/0098_passkey_credentials.down.sql")
	if err != nil {
		t.Fatalf("read migration down: %v", err)
	}
	raw := string(down)
	if !strings.Contains(raw, "IF EXISTS (SELECT 1 FROM passkey_credentials)") {
		t.Fatalf("down migration must refuse rollback when passkey credentials exist:\n%s", raw)
	}
	if !strings.Contains(raw, "Owner-gated account-security data plan") {
		t.Fatalf("down migration must explain Owner-gated rollback requirement:\n%s", raw)
	}
}
