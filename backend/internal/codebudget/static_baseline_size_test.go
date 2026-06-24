package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestStaticAnalysisBaselinesDoNotGrow(t *testing.T) {
	backendRoot := filepath.Join("..", "..")
	cases := []struct {
		name     string
		path     string
		maxLines int
	}{
		{
			name:     "staticcheck",
			path:     filepath.Join(backendRoot, "scripts", "staticcheck-baseline.txt"),
			maxLines: 93,
		},
		{
			name:     "deadcode",
			path:     filepath.Join(backendRoot, "scripts", "deadcode-baseline.txt"),
			maxLines: 787,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := readBaselineLines(t, tc.path)
			if len(lines) > tc.maxLines {
				t.Fatalf("%s baseline 膨胀到 %d 行,超过当前上限 %d 行;请优先修掉新增 finding,不要扩大祖父豁免池", tc.name, len(lines), tc.maxLines)
			}
			if !sort.StringsAreSorted(lines) {
				t.Fatalf("%s baseline 必须保持排序,避免重复项和噪声 diff", tc.name)
			}
			for i := 1; i < len(lines); i++ {
				if lines[i] == lines[i-1] {
					t.Fatalf("%s baseline 存在重复项 %q", tc.name, lines[i])
				}
			}
		})
	}
}

func readBaselineLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 baseline %s: %v", path, err)
	}
	text := strings.TrimRight(string(raw), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
