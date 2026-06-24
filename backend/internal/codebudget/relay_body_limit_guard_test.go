package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var relayBodyLimitPackages = map[string]struct{}{
	"internal/completionshttp": {},
	"internal/embeddingshttp":  {},
	"internal/geminihttp":      {},
	"internal/imageshttp":      {},
	"internal/rerankhttp":      {},
}

func TestRelayHandlersUseSharedRequestBodyLimit(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string
	for relDir := range relayBodyLimitPackages {
		dir := filepath.Join(root, filepath.FromSlash(relDir))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("读取 %s: %v", relDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读取 %s: %v", path, err)
			}
			rel := filepath.ToSlash(filepath.Join(relDir, entry.Name()))
			text := string(raw)
			for i, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "maxRequestBodyBytes") {
					violations = append(violations, rel+":"+strconv.Itoa(i+1)+": 兄弟 relay handler 禁止重新声明本地 maxRequestBodyBytes,应使用 relaybody.RequestBodyLimit()")
				}
				if strings.Contains(line, "relaybody.ReadLimitedRequestBody") && !strings.Contains(line, "relaybody.RequestBodyLimit()") {
					violations = append(violations, rel+":"+strconv.Itoa(i+1)+": ReadLimitedRequestBody 必须使用 relaybody.RequestBodyLimit()")
				}
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("relay 请求体上限共享检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
