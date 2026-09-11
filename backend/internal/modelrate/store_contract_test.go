package modelrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSourcesDoNotTouchUsageRecords(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), "usage_records") {
			t.Fatalf("%s mentions usage_records; writing an override must not rewrite settled bills", name)
		}
	}
}
