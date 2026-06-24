package codebudget

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type integrationPGWorkflowStep struct {
	relPath   string
	startLine int
	stepText  string
	jobHeader string
}

func TestIntegrationPGCIUsesRuntimeDatabaseURL(t *testing.T) {
	backendRoot := filepath.Join("..", "..")
	repoRoot := filepath.Clean(filepath.Join(backendRoot, ".."))
	workflowRoot := filepath.Join(repoRoot, ".github", "workflows")
	entries, err := os.ReadDir(workflowRoot)
	if err != nil {
		t.Fatalf("读取 GitHub Actions workflow: %v", err)
	}
	var steps []integrationPGWorkflowStep
	for _, entry := range entries {
		if entry.IsDir() || !isWorkflowYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(workflowRoot, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 workflow %s: %v", path, err)
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("计算 workflow 相对路径 %s: %v", path, err)
		}
		steps = append(steps, integrationPGWorkflowSteps(filepath.ToSlash(rel), string(raw))...)
	}
	if len(steps) == 0 {
		t.Fatalf("CI 必须至少有一个 go test -tags=integration_pg 步骤，避免集成测试只在本地存在而主 CI 不编译不运行")
	}
	var violations []string
	for _, step := range steps {
		if runtimeDatabaseEnvConfigured(step.stepText) || runtimeDatabaseEnvConfigured(step.jobHeader) {
			continue
		}
		violations = append(violations, step.relPath+":"+strconv.Itoa(step.startLine)+": integration_pg 步骤必须配置 HUAKAI_DATABASE_URL，不能只依赖 HUAKAI_TEST_DATABASE_URL")
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("integration_pg CI 数据库环境检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func integrationPGWorkflowSteps(relPath, text string) []integrationPGWorkflowStep {
	lines := strings.Split(text, "\n")
	var steps []integrationPGWorkflowStep
	for i := range lines {
		if !isWorkflowStepHeader(lines[i]) {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if isWorkflowStepHeader(lines[j]) {
				end = j
				break
			}
		}
		stepText := strings.Join(lines[i:end], "\n")
		if !hasIntegrationPGGoTest(stepText) {
			continue
		}
		steps = append(steps, integrationPGWorkflowStep{
			relPath:   relPath,
			startLine: i + 1,
			stepText:  stepText,
			jobHeader: workflowJobHeader(lines, i),
		})
	}
	return steps
}

func workflowJobHeader(lines []string, stepIndex int) string {
	start := 0
	for i := stepIndex; i >= 0; i-- {
		if isWorkflowJobHeader(lines[i]) {
			start = i
			break
		}
	}
	end := stepIndex
	for i := start + 1; i < stepIndex; i++ {
		if strings.TrimSpace(lines[i]) == "steps:" {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func isWorkflowYAML(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".yml" || ext == ".yaml"
}

func isWorkflowStepHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "- name:")
}

func isWorkflowJobHeader(line string) bool {
	if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	return strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-")
}

func hasIntegrationPGGoTest(text string) bool {
	return strings.Contains(text, "go test") &&
		(strings.Contains(text, "-tags=integration_pg") || strings.Contains(text, "-tags integration_pg"))
}

func runtimeDatabaseEnvConfigured(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "HUAKAI_DATABASE_URL") {
			return true
		}
	}
	return false
}
