package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v2"
)

func TestAuthoritativeMatrixMatchesOpenAPI(t *testing.T) {
	root := repoRoot(t)
	rendered, err := renderFile(filepath.Join(root, defaultSpecPath))
	if err != nil {
		t.Fatalf("生成矩阵：%v", err)
	}
	current, err := os.ReadFile(filepath.Join(root, defaultDocPath))
	if err != nil {
		t.Fatalf("读取权威矩阵：%v", err)
	}
	if string(current) != string(rendered) {
		t.Fatal("前后端双向追踪矩阵已过期，请运行 go run ./cmd/frontend-traceability -write")
	}
}

func TestBuildRowsRejectsDuplicateOperationID(t *testing.T) {
	ops := []operation{
		{Method: "GET", Path: "/a", OperationID: "same", Tags: []string{"health"}},
		{Method: "GET", Path: "/b", OperationID: "same", Tags: []string{"health"}},
	}
	if _, err := buildRows(ops); err == nil || !strings.Contains(err.Error(), "operationId 重复") {
		t.Fatalf("重复 operationId 未被拒绝：%v", err)
	}
}

func TestBuildRowsRejectsUnknownTag(t *testing.T) {
	ops := []operation{{
		Method: "GET", Path: "/unknown", OperationID: "unknown", Tags: []string{"not-classified"},
	}}
	if _, err := buildRows(ops); err == nil || !strings.Contains(err.Error(), "未归类 tag") {
		t.Fatalf("未知 tag 未被拒绝：%v", err)
	}
}

func TestBuildRowsRejectsAdminOperationWithoutExplicitRole(t *testing.T) {
	ops := []operation{{
		Method: "GET", Path: "/v1/admin/example", OperationID: "adminExample", Tags: []string{"admin"},
	}}
	if _, err := buildRows(ops); err == nil || !strings.Contains(err.Error(), "缺少 x-huakai-required-role") {
		t.Fatalf("缺少显式角色的管理 operation 未被拒绝：%v", err)
	}
}

func TestBuildRowsRejectsUnknownRequiredRole(t *testing.T) {
	ops := []operation{{
		Method:       "GET",
		Path:         "/v1/admin/example",
		OperationID:  "adminExample",
		Tags:         []string{"admin"},
		RequiredRole: "tenant_operatr",
	}}
	if _, err := buildRows(ops); err == nil || !strings.Contains(err.Error(), "未知 x-huakai-required-role") {
		t.Fatalf("未知角色扩展值未被拒绝：%v", err)
	}
}

func TestRenderedMatrixCoversEveryOperationOnce(t *testing.T) {
	root := repoRoot(t)
	rendered, err := renderFile(filepath.Join(root, defaultSpecPath))
	if err != nil {
		t.Fatalf("生成矩阵：%v", err)
	}
	var doc openAPIDocument
	raw, err := os.ReadFile(filepath.Join(root, defaultSpecPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.UnmarshalStrict(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, op := range flatten(doc) {
		needle := "| `RTM:" + op.OperationID + "` |"
		if got := strings.Count(string(rendered), needle); got != 1 {
			t.Fatalf("%s 的稳定追踪键在矩阵中出现 %d 次，期望 1 次", op.OperationID, got)
		}
	}
}

func TestFrontendVisibleAdminOperationsDeclareRuntimeRoles(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, defaultSpecPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc openAPIDocument
	if err := yaml.UnmarshalStrict(raw, &doc); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]operation)
	for _, op := range flatten(doc) {
		byID[op.OperationID] = op
	}
	expected := map[string]string{
		"getAdminUsageOverview":              "platform_admin",
		"getAdminUsageProviderAccountCounts": "platform_admin",
		"getAdminUsageLeaderboard":           "platform_admin",
		"getAdminUsagePerformance":           "platform_admin",
		"getAdminUsagePerfMetricsSummary":    "platform_admin",
		"getAdminUsagePerfMetricsByBucket":   "platform_admin",
		"getAdminUsageHealthScore":           "platform_admin",
		"listAdminRuntimeLogs":               "platform_admin",
		"cleanupAdminRuntimeLogs":            "platform_admin",
		"getAdminRuntimeLogSinkHealth":       "platform_admin",
		"getAdminModules":                    "platform_admin",
		"getAdminModulesAlias":               "platform_admin",
		"listAdminCostDisputes":              "tenant_scoped_admin",
		"resolveCostDispute":                 "tenant_scoped_admin",
		"getAdminTenantWallet":               "tenant_scoped_admin",
		"listAdminBalanceTransactions":       "tenant_scoped_admin",
		"adminExportOrdersCSV":               "tenant_scoped_admin",
		"adminExportRefundsCSV":              "tenant_scoped_admin",
		"getAdminEmailSettings":              "tenant_scoped_admin",
		"updateAdminEmailSettings":           "tenant_scoped_admin",
		"testAdminEmailSettings":             "tenant_scoped_admin",
		"previewAdminEmailTemplate":          "tenant_scoped_admin",
	}
	for operationID, role := range expected {
		op, ok := byID[operationID]
		if !ok {
			t.Errorf("OpenAPI 缺少 operationId %s", operationID)
			continue
		}
		if op.RequiredRole != role {
			t.Errorf("%s 角色=%q，期望 %q", operationID, op.RequiredRole, role)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
