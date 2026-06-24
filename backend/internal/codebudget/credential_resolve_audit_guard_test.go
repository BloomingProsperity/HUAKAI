package codebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialResolveAuditBestEffortIsExplicit(t *testing.T) {
	path := filepath.Join("..", "credentialstore", "postgres_store.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentialstore postgres store: %v", err)
	}
	src := string(raw)
	if strings.Contains(src, "_ = s.InsertAuditEvent(ctx, AuditEvent{") {
		t.Fatalf("ResolveActive audit must use the named best-effort helper, not a bare ignored InsertAuditEvent")
	}
	for _, required := range []string{
		"func (s *Store) recordCredentialResolvedAuditBestEffort",
		"s.recordCredentialResolvedAuditBestEffort(ctx, rec)",
		"insertAuditEventStrict",
	} {
		if !strings.Contains(src, required) {
			t.Fatalf("credential resolve audit guard missing %q", required)
		}
	}
}
