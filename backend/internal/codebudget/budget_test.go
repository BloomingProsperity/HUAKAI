// 包 codebudget — 反 god-package 软预算门(冻结规则 2026-06-10 软化的落地)。
//
// 背景:internal/{gatewayhttp,gateway,proto} 的「禁新增文件」硬冻结实测失败——
// freeze 只挡新文件,把新逻辑逼进旧文件致其膨胀(gatewayhttp 33K 行/0 子包)。
// 本门改为软预算:非测试 Go 文件 ≤ maxFileLines 行,单目录包非测试 ≤
// maxPackageLines 行 / maxPackageFiles 文件;存量超标项按当前体量入基线豁免
// (baseline.json),只挡继续增长(基线 +growthAllowance)。超标 = 拆子包
// (范本:internal/provider 的 9 核心 + 13 子包),不是回去塞旧文件。
//
// 基线再生成:HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1 go test ./internal/codebudget/
// (仅在有意拆分/重构后使用;CI/双门禁止带该 env)。
package codebudget

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	maxFileLines    = 600
	maxPackageLines = 6000
	maxPackageFiles = 20
	// growthAllowance 是基线豁免项的增长余量(比例)。正常 bug-fix/加性 edit
	// 在余量内;余量耗尽 = 该把新逻辑拆出去了。
	growthAllowance = 0.05
)

var forbiddenCommentTerms = []string{
	"sub2api",
	"sub2-api",
	"sub2",
	"new-api",
	"cliproxyapi",
	"cli-proxy-api",
	"cli proxy api",
	"litellm",
	"portkey",
	"helicone",
	"all-api-hub",
	"all api hub",
	"one-api",
	"envoy ai gateway",
	"aiclient-2-api",
	"三镜",
	"参考项目",
	"借鉴项目",
}

type baseline struct {
	// Files: 相对 internal/ 的文件路径 → 入基线时的行数。
	Files map[string]int `json:"files"`
	// Packages: 相对 internal/ 的目录 → 入基线时的 [行数, 文件数]。
	Packages map[string][2]int `json:"packages"`
}

func TestFileAndPackageBudgets(t *testing.T) {
	root := filepath.Join("..", "..") // backend/
	internalDir := filepath.Join(root, "internal")

	fileLines := map[string]int{}
	pkgLines := map[string]int{}
	pkgFiles := map[string]int{}

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
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
		lines := strings.Count(string(raw), "\n") + 1
		rel, err := filepath.Rel(internalDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		fileLines[rel] = lines
		pkgLines[dir] += lines
		pkgFiles[dir]++
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	if len(fileLines) < 100 {
		t.Fatalf("scanned %d files want >=100(扫描面异常缩水,root 解析错了?)", len(fileLines))
	}

	if os.Getenv("HUAKAI_REWRITE_CODE_BUDGET_BASELINE") == "1" {
		writeBaseline(t, fileLines, pkgLines, pkgFiles)
		return
	}

	base := readBaseline(t)
	var violations []string

	grown := func(now, base int) bool {
		return float64(now) > float64(base)*(1+growthAllowance)
	}

	for rel, lines := range fileLines {
		if lines <= maxFileLines {
			continue
		}
		if baseLines, ok := base.Files[rel]; ok {
			if grown(lines, baseLines) {
				violations = append(violations, fmt.Sprintf(
					"file %s: %d 行,超基线 %d 的 %.0f%% 余量——把新逻辑拆到子包,别再喂它", rel, lines, baseLines, growthAllowance*100))
			}
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"file %s: %d 行 > %d(非豁免)——拆文件/子包", rel, lines, maxFileLines))
	}

	for dir, lines := range pkgLines {
		files := pkgFiles[dir]
		if lines <= maxPackageLines && files <= maxPackageFiles {
			continue
		}
		if b, ok := base.Packages[dir]; ok {
			if grown(lines, b[0]) || grown(files, b[1]) {
				violations = append(violations, fmt.Sprintf(
					"package %s: %d 行/%d 文件,超基线 [%d, %d] 的 %.0f%% 余量——按职责拆子包(范本 internal/provider)", dir, lines, files, b[0], b[1], growthAllowance*100))
			}
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"package %s: %d 行/%d 文件 > [%d 行, %d 文件](非豁免)——按职责拆子包", dir, lines, files, maxPackageLines, maxPackageFiles))
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("代码体量软预算超标 %d 项:\n%s\n(确属有意重构后体量下降可再生成基线:HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1)", len(violations), strings.Join(violations, "\n"))
	}
}

func TestGoCommentsDoNotNameReferenceProjects(t *testing.T) {
	root := filepath.Join("..", "..") // backend/
	fset := token.NewFileSet()
	checked := 0
	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && (info.Name() == "vendor" || strings.HasPrefix(info.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		checked++
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if term, found := forbiddenTermInComment(comment.Text); found {
					pos := fset.Position(comment.Pos())
					rel, relErr := filepath.Rel(root, path)
					if relErr != nil {
						return relErr
					}
					violations = append(violations, fmt.Sprintf("%s:%d: 注释包含禁止词 %q", filepath.ToSlash(rel), pos.Line, term))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 Go 注释: %v", err)
	}
	if checked < 100 {
		t.Fatalf("只检查了 %d 个 Go 文件,扫描面异常缩水", checked)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("代码注释必须只描述 HUAKAI 自身机制,%d 处违规:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func TestForbiddenTermInComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		found   bool
	}{
		{name: "拦截英文项目名", comment: "// Sub2API behavior", found: true},
		{name: "拦截中文对照标签", comment: "// 参考项目的做法", found: true},
		{name: "允许普通 API 描述", comment: "// create a new API key", found: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := forbiddenTermInComment(tt.comment)
			if found != tt.found {
				t.Fatalf("found = %v, want %v", found, tt.found)
			}
		})
	}
}

func forbiddenTermInComment(comment string) (string, bool) {
	lower := strings.ToLower(comment)
	for _, term := range forbiddenCommentTerms {
		if strings.Contains(lower, term) {
			return term, true
		}
	}
	return "", false
}

func writeBaseline(t *testing.T, fileLines, pkgLines, pkgFiles map[string]int) {
	t.Helper()
	b := baseline{Files: map[string]int{}, Packages: map[string][2]int{}}
	for rel, lines := range fileLines {
		if lines > maxFileLines {
			b.Files[rel] = lines
		}
	}
	for dir, lines := range pkgLines {
		if lines > maxPackageLines || pkgFiles[dir] > maxPackageFiles {
			b.Packages[dir] = [2]int{lines, pkgFiles[dir]}
		}
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	if err := os.WriteFile("baseline.json", append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	t.Logf("baseline.json 重写:%d 个文件豁免,%d 个包豁免", len(b.Files), len(b.Packages))
}

func readBaseline(t *testing.T) baseline {
	t.Helper()
	raw, err := os.ReadFile("baseline.json")
	if err != nil {
		t.Fatalf("read baseline.json(基线缺失;首次生成用 HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1): %v", err)
	}
	var b baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse baseline.json: %v", err)
	}
	return b
}
