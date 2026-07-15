package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
)

// 本测试同时锁住 chi operation 与 OpenAPI schema，避免只声明 path 却漏掉方法或账号数组。
func TestModelRoutingOverrideRoutesAndOpenAPISchemasStayInSync(t *testing.T) {
	router := buildTestRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/model-routing-overrides?tenant_id=1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatal("无尾斜杠的 collection 路径未命中生产路由")
	}
	implementation := openapicheck.WalkChiOperations(router)
	specPath, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 OpenAPI 路径：%v", err)
	}
	specification, err := openapicheck.ParseSpecOperations(specPath)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations：%v", err)
	}

	operations := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/v1/model-routing-overrides"},
		{http.MethodPost, "/admin/v1/model-routing-overrides"},
		{http.MethodPatch, "/admin/v1/model-routing-overrides/{id}"},
		{http.MethodDelete, "/admin/v1/model-routing-overrides/{id}"},
	}
	for _, operation := range operations {
		if !hasOperationEquivalent(implementation, operation.method, operation.path) {
			t.Errorf("chi 缺少 %s %s", operation.method, operation.path)
		}
		if !hasOperationEquivalent(specification, operation.method, operation.path) {
			t.Errorf("OpenAPI 缺少 %s %s", operation.method, operation.path)
		}
	}

	content, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("读取 OpenAPI：%v", err)
	}
	text := string(content)
	responseSchema := openAPIComponentBlock(t, text, "ModelRoutingOverride", "ModelRoutingOverrideCreateRequest")
	for _, snippet := range []string{"required: [id, pool_group_id, model, provider_account_ids, enabled, created_at, updated_at]", "provider_account_ids:", "uniqueItems: true"} {
		if !strings.Contains(responseSchema, snippet) {
			t.Errorf("ModelRoutingOverride 缺少 %q", snippet)
		}
	}
	createSchema := openAPIComponentBlock(t, text, "ModelRoutingOverrideCreateRequest", "ModelRoutingOverrideUpdateRequest")
	for _, snippet := range []string{"required: [pool_group_id, model, provider_account_ids]", "minItems: 1", "default: true"} {
		if !strings.Contains(createSchema, snippet) {
			t.Errorf("ModelRoutingOverrideCreateRequest 缺少 %q", snippet)
		}
	}
	updateSchema := openAPIComponentBlock(t, text, "ModelRoutingOverrideUpdateRequest", "Proxy")
	for _, snippet := range []string{"minProperties: 1", "provider_account_ids:", "minItems: 1", "enabled:"} {
		if !strings.Contains(updateSchema, snippet) {
			t.Errorf("ModelRoutingOverrideUpdateRequest 缺少 %q", snippet)
		}
	}
}

func openAPIComponentBlock(t *testing.T, specification, name, next string) string {
	t.Helper()
	startMarker := "\n    " + name + ":\n"
	start := strings.Index(specification, startMarker)
	if start < 0 {
		t.Fatalf("OpenAPI 缺少 component %s", name)
	}
	start += len(startMarker)
	end := strings.Index(specification[start:], "\n    "+next+":\n")
	if end < 0 {
		t.Fatalf("OpenAPI component %s 后缺少边界 %s", name, next)
	}
	return specification[start : start+end]
}
