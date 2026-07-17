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

// 路由与 OpenAPI 必须同时包含五个模型主体 operation；删除任一侧声明都会转红。
func TestModelAdminRoutesAndOpenAPIStayInSync(t *testing.T) {
	router := buildTestRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/models?scope=global", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatal("无尾斜杠的模型主体 collection 路径未命中生产路由")
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
		{http.MethodGet, "/v1/admin/models"},
		{http.MethodPost, "/v1/admin/models"},
		{http.MethodGet, "/v1/admin/models/{id}"},
		{http.MethodPatch, "/v1/admin/models/{id}"},
		{http.MethodDelete, "/v1/admin/models/{id}"},
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
	modelSchema := openAPIComponentBlock(t, text, "AdminModel", "AdminModelListResponse")
	for _, snippet := range []string{
		"id: { type: integer, format: int64, minimum: 1 }",
		"tenant_id:", "scope:", "canonical_id:", "protocol_family:",
		"enum: [anthropic_messages, openai_chat, openai_responses, gemini]",
		"default_provider_model_id:", "default_context_window:",
		"default_request_timeout_ms:", "pricing_class:", "model_owner:", "status:",
	} {
		if !strings.Contains(modelSchema, snippet) {
			t.Errorf("AdminModel 缺少 %q", snippet)
		}
	}
	createSchema := openAPIComponentBlock(t, text, "AdminModelCreateRequest", "AdminModelUpdateRequest")
	for _, snippet := range []string{
		"required: [canonical_id, protocol_family, default_provider_model_id]",
		"default_request_timeout_ms:", "default: 60000", "default: active",
	} {
		if !strings.Contains(createSchema, snippet) {
			t.Errorf("AdminModelCreateRequest 缺少 %q", snippet)
		}
	}
	updateSchema := openAPIComponentBlock(t, text, "AdminModelUpdateRequest", "ModelCapabilitiesUpdateRequest")
	for _, snippet := range []string{"minProperties: 1", "protocol_family:", "status: { type: string, enum: [active, disabled] }"} {
		if !strings.Contains(updateSchema, snippet) {
			t.Errorf("AdminModelUpdateRequest 缺少 %q", snippet)
		}
	}
}
