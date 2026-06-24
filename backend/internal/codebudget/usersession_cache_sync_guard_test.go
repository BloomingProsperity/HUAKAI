package codebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserSessionCacheSyncErrorsAreNamedBestEffort(t *testing.T) {
	path := filepath.Join("..", "usersession", "store.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usersession store: %v", err)
	}
	src := string(raw)
	for _, forbidden := range []string{
		"_ = s.cache.",
		"_, _ = s.cache.",
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("usersession PostgresStore must route cache sync errors through syncCacheBestEffort, found %q", forbidden)
		}
	}
	for _, required := range []string{
		"func (s *PostgresStore) syncCacheBestEffort",
		"PostgreSQL 是权威源",
		"s.syncCacheBestEffort(func(cache *MemoryStore) error",
	} {
		if !strings.Contains(src, required) {
			t.Fatalf("usersession cache sync guard missing %q", required)
		}
	}
}
