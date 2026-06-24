package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var allowedUntaggedDatabaseSkipTests = map[string]string{
	"cmd/gateway/frontend_wiring_test.go":                      "smoke build tag",
	"cmd/gateway/smoke_test.go":                                "smoke build tag",
}

func TestDatabaseURLSkipTestsAreTaggedOrExplicitDebt(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string
	seenAllowed := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if !strings.Contains(text, "HUAKAI_DATABASE_URL not set") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "internal/codebudget/integration_pg_skip_guard_test.go" {
			return nil
		}
		if databaseSkipTestHasAcceptedBuildTag(text) {
			return nil
		}
		if _, ok := allowedUntaggedDatabaseSkipTests[rel]; ok {
			seenAllowed[rel] = true
			return nil
		}
		line := firstDatabaseSkipLine(text)
		violations = append(violations, rel+":"+strconv.Itoa(line)+": HUAKAI_DATABASE_URL skip 测试必须有 integration_pg/smoke build tag,或先列入显式拆分债务白名单")
		return nil
	})
	if err != nil {
		t.Fatalf("scan database skip tests: %v", err)
	}
	for rel := range allowedUntaggedDatabaseSkipTests {
		if !seenAllowed[rel] && !allowedSkipHasAcceptedBuildTag(t, root, rel) {
			violations = append(violations, rel+": 白名单项已不再命中,请删除白名单或补充真实原因")
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("数据库 skip 测试 build tag 检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func databaseSkipTestHasAcceptedBuildTag(text string) bool {
	lines := strings.Split(text, "\n")
	limit := 8
	if len(lines) < limit {
		limit = len(lines)
	}
	for _, line := range lines[:limit] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//go:build ") &&
			(strings.Contains(trimmed, "integration_pg") || strings.Contains(trimmed, "smoke")) {
			return true
		}
	}
	return false
}

func allowedSkipHasAcceptedBuildTag(t *testing.T, root, rel string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "HUAKAI_DATABASE_URL not set") && databaseSkipTestHasAcceptedBuildTag(string(raw))
}

func firstDatabaseSkipLine(text string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "HUAKAI_DATABASE_URL not set") {
			return i + 1
		}
	}
	return 0
}
