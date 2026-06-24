package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestAutomationDoesNotRewriteBaselines(t *testing.T) {
	backendRoot := filepath.Join("..", "..")
	repoRoot := filepath.Clean(filepath.Join(backendRoot, ".."))
	scanRoots := []string{
		filepath.Join(repoRoot, ".github"),
		filepath.Join(repoRoot, "scripts"),
		filepath.Join(backendRoot, "scripts"),
	}
	scanFiles := []string{
		filepath.Join(repoRoot, "Makefile"),
		filepath.Join(backendRoot, "Makefile"),
	}
	var violations []string
	for _, root := range scanRoots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		if err := scanAutomationRoot(repoRoot, root, &violations); err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
	for _, path := range scanFiles {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := scanAutomationFile(repoRoot, path, &violations); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("基线重写不得出现在 CI/普通脚本 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func scanAutomationRoot(repoRoot, root string, violations *[]string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !isAutomationTextFile(path) || isDedicatedQualityGateScript(path) {
			return nil
		}
		return scanAutomationFile(repoRoot, path, violations)
	})
}

func scanAutomationFile(repoRoot, path string, violations *[]string) error {
	if isDedicatedQualityGateScript(path) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "HUAKAI_REWRITE_CODE_BUDGET_BASELINE") {
			*violations = append(*violations, rel+":"+strconv.Itoa(i+1)+": 禁止在自动化入口设置 HUAKAI_REWRITE_CODE_BUDGET_BASELINE")
		}
		if strings.Contains(line, "quality-gate.sh") && strings.Contains(line, "--update") {
			*violations = append(*violations, rel+":"+strconv.Itoa(i+1)+": 禁止在自动化入口调用 quality-gate.sh --update")
		}
		if strings.Contains(line, "staticcheck-baseline.txt") {
			*violations = append(*violations, rel+":"+strconv.Itoa(i+1)+": 禁止在自动化入口直接触碰 staticcheck-baseline.txt")
		}
		if strings.Contains(line, "deadcode-baseline.txt") {
			*violations = append(*violations, rel+":"+strconv.Itoa(i+1)+": 禁止在自动化入口直接触碰 deadcode-baseline.txt")
		}
	}
	return nil
}

func isAutomationTextFile(path string) bool {
	switch filepath.Ext(path) {
	case ".yml", ".yaml", ".sh", ".mk":
		return true
	}
	base := filepath.Base(path)
	return base == "Makefile" || base == "makefile"
}

func isDedicatedQualityGateScript(path string) bool {
	return filepath.ToSlash(path) == filepath.ToSlash(filepath.Join("..", "..", "scripts", "quality-gate.sh")) ||
		strings.HasSuffix(filepath.ToSlash(path), "/backend/scripts/quality-gate.sh")
}
