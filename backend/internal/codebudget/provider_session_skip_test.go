package codebudget

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProviderSessionTestsDoNotUseSkipPlaceholders(t *testing.T) {
	root := filepath.Join("..", "..")
	providerRoot := filepath.Join(root, "internal", "provider")
	fset := token.NewFileSet()
	var violations []string
	targets := map[string]struct{}{
		filepath.ToSlash(filepath.Join(providerRoot, "antigravity", "antigravity_session_test.go")): {},
		filepath.ToSlash(filepath.Join(providerRoot, "copilot", "copilot_session_test.go")):           {},
		filepath.ToSlash(filepath.Join(providerRoot, "cursor", "cursor_session_test.go")):             {},
		filepath.ToSlash(filepath.Join(providerRoot, "gemini", "gemini_advanced_session_test.go")):    {},
		filepath.ToSlash(filepath.Join(providerRoot, "kiro", "kiro_session_test.go")):                 {},
		filepath.ToSlash(filepath.Join(providerRoot, "windsurf", "windsurf_session_test.go")):         {},
	}

	err := filepath.Walk(providerRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if _, ok := targets[filepath.ToSlash(path)]; !ok {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if !strings.Contains(text, "TODO(provider-session-response)") {
			violations = append(violations, relProviderTestPath(root, path)+": 缺少 provider-session-response TODO,不能把未接真实响应层的 reauth 覆盖伪装成已完成")
		}
		if !strings.Contains(text, "TODO(dispatcher-channel-health)") {
			violations = append(violations, relProviderTestPath(root, path)+": 缺少 dispatcher-channel-health TODO,不能把未接真实响应层的 5xx/DLQ 覆盖伪装成已完成")
		}
		parsed, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "t" {
				return true
			}
			if selector.Sel.Name != "Skip" && selector.Sel.Name != "Skipf" {
				return true
			}
			pos := fset.Position(selector.Pos())
			violations = append(violations, rel+":"+strconv.Itoa(pos.Line)+": provider session 测试禁止用 t."+selector.Sel.Name+" 占位")
			return true
		})
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			switch fn.Name.Name {
			case "TestAntigravitySessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestAntigravitySessionAdapter_Upstream5xxEnqueuesDLQRetry",
				"TestCopilotSessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestCopilotSessionAdapter_Upstream5xxEnqueuesDLQRetry",
				"TestCursorSessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestCursorSessionAdapter_Upstream5xxEnqueuesDLQRetry",
				"TestGeminiAdvancedSessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestGeminiAdvancedSessionAdapter_Upstream5xxEnqueuesDLQRetry",
				"TestKiroSessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestKiroSessionAdapter_Upstream5xxEnqueuesDLQRetry",
				"TestWindsurfSessionAdapter_ExpiredSessionTriggersReauthFlow",
				"TestWindsurfSessionAdapter_Upstream5xxEnqueuesDLQRetry":
				pos := fset.Position(fn.Pos())
				violations = append(violations, rel+":"+strconv.Itoa(pos.Line)+": 禁止保留空 provider session 覆盖函数 "+fn.Name.Name+";未接真实响应层时应写显式 TODO")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan provider session tests: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("provider session skip 占位检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func relProviderTestPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
