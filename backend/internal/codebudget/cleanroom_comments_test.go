package codebudget

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestGoCommentsAvoidExternalProjectMarkers(t *testing.T) {
	root := filepath.Join("..", "..")
	forbidden := forbiddenExternalProjectMarkers()

	fset := token.NewFileSet()
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				text := strings.ToLower(comment.Text)
				for _, term := range forbidden {
					if !strings.Contains(text, term) {
						continue
					}
					pos := fset.Position(comment.Pos())
					violations = append(violations, rel+":"+strconv.Itoa(pos.Line)+": Go 注释包含 clean-room 禁用标记 "+term)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go comments: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Go 注释 clean-room 检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func forbiddenExternalProjectMarkers() []string {
	parts := [][]string{
		{"sub", "2api"},
		{"new", "-api"},
		{"cliproxy", "api"},
		{"all-api", "-hub"},
		{"one", "-api"},
		{"lite", "llm"},
		{"port", "key"},
		{"heli", "cone"},
		{"envoy ai", " gateway"},
		{"aiclient", "-2-api"},
		{"grok", "2api"},
	}
	markers := make([]string, 0, len(parts)+2)
	for _, part := range parts {
		markers = append(markers, strings.Join(part, ""))
	}
	markers = append(markers, "借"+"鉴", "参考"+"某项目")
	return markers
}
