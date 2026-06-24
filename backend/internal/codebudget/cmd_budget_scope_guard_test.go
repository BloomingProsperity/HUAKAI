package codebudget

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCodeBudgetIncludesCmdGatewayScope(t *testing.T) {
	rawTest, err := os.ReadFile("budget_test.go")
	if err != nil {
		t.Fatalf("read budget test: %v", err)
	}
	testSrc := string(rawTest)
	for _, required := range []string{
		`filepath.Join(root, "cmd")`,
		`prefix: "cmd/"`,
		`scanBudgetRoot`,
	} {
		if !strings.Contains(testSrc, required) {
			t.Fatalf("codebudget scan scope missing %q", required)
		}
	}

	rawBaseline, err := os.ReadFile("baseline.json")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var b baseline
	if err := json.Unmarshal(rawBaseline, &b); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	for _, file := range []string{
		"cmd/gateway/wiring.go",
		"cmd/gateway/routes.go",
	} {
		if _, ok := b.Files[file]; !ok {
			t.Fatalf("baseline missing cmd gateway file %q", file)
		}
	}
	if _, ok := b.Packages["cmd/gateway"]; !ok {
		t.Fatal("baseline missing cmd/gateway package budget")
	}
}
