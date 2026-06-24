package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProductionTickersHaveExplicitStopPath(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if line := firstLineContaining(text, "time.Tick("); line > 0 {
			violations = append(violations, rel+":"+strconv.Itoa(line)+": time.Tick 无法显式 Stop,请改用 time.NewTicker 并在退出路径 Stop")
		}
		if line := firstLineContaining(text, "time.NewTicker("); line > 0 && !strings.Contains(text, ".Stop()") {
			violations = append(violations, rel+":"+strconv.Itoa(line)+": time.NewTicker 缺少同文件显式 Stop 路径")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan ticker lifecycle: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("ticker 生命周期检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func firstLineContaining(text, needle string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}
