package openapicheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v2"
)

var pathTemplateVariable = regexp.MustCompile(`\{([^{}]+)\}`)

func TestAuthoritativeSpecHasNoUnresolvedLocalReferences(t *testing.T) {
	document := loadSpecDocument(t, filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml"))
	unresolved := unresolvedLocalReferences(document)
	if len(unresolved) != 0 {
		t.Fatalf("主 OpenAPI 存在未解析的本地引用:\n%s", strings.Join(unresolved, "\n"))
	}
}

func TestUnresolvedLocalReferencesDetectsMissingTarget(t *testing.T) {
	document := parseSpecDocument(t, `
openapi: 3.1.0
paths:
  /v1/items:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Missing"
components:
  schemas:
    Existing:
      type: object
`)
	got := unresolvedLocalReferences(document)
	want := []string{"#/components/schemas/Missing"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("未解析引用=%v，期望 %v", got, want)
	}
}

func TestAuthoritativeSpecDeclaresEveryOperationPathParameter(t *testing.T) {
	document := loadSpecDocument(t, filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml"))
	missing := missingOperationPathParameters(document)
	if len(missing) != 0 {
		t.Fatalf("主 OpenAPI 有操作未声明路径参数:\n%s", strings.Join(missing, "\n"))
	}
}

func TestEveryBrowserSessionIssuerDeclaresRuntimeContract(t *testing.T) {
	document := loadSpecDocument(t, filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml"))
	issuers := []struct {
		path   string
		method string
	}{
		{path: "/v1/auth/login", method: "post"},
		{path: "/v1/auth/login/2fa", method: "post"},
		{path: "/v1/auth/passkey/login/finish", method: "post"},
		{path: "/v1/auth/oauth-callback", method: "post"},
		{path: "/v1/auth/oauth-pending/complete", method: "post"},
		{path: "/v1/auth/telegram-login", method: "post"},
		{path: "/v1/sessions/refresh", method: "post"},
	}
	for _, issuer := range issuers {
		operation := mustOperation(t, document, issuer.path, issuer.method)
		if !operationDeclaresParameter(
			document,
			issuer.path,
			issuer.method,
			"X-HUAKAI-Session-Mode",
			"header",
		) {
			t.Errorf("%s %s 缺少浏览器会话模式参数", strings.ToUpper(issuer.method), issuer.path)
		}
		if !successfulResponseReferences(
			document,
			operation,
			"#/components/schemas/SessionTokenBundle",
		) {
			t.Errorf("%s %s 的成功响应未接入统一会话令牌合同", strings.ToUpper(issuer.method), issuer.path)
		}
	}

	refresh := mustOperation(t, document, "/v1/sessions/refresh", "post")
	if !operationDeclaresParameter(
		document,
		"/v1/sessions/refresh",
		"post",
		"X-HUAKAI-CSRF",
		"header",
	) {
		t.Error("浏览器刷新缺少 CSRF 参数合同")
	}
	responses, _ := refresh["responses"].(map[interface{}]interface{})
	for _, status := range []string{"200", "400", "401", "403", "409", "503"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("浏览器刷新缺少运行时状态 %s；删除该声明会让生成客户端无法区分恢复动作", status)
		}
	}

	rawBundle, ok := resolveLocalReference(document, "#/components/schemas/SessionTokenBundle")
	if !ok {
		t.Fatal("OpenAPI 缺少统一会话令牌合同")
	}
	bundle, _ := rawBundle.(map[interface{}]interface{})
	required, _ := bundle["required"].([]interface{})
	for _, field := range []string{
		"session_token",
		"session_expires_at",
		"refresh_expires_at",
		"family",
		"generation",
	} {
		if !containsString(required, field) {
			t.Errorf("统一会话令牌合同缺少必填字段 %s", field)
		}
	}
	oneOf, _ := bundle["oneOf"].([]interface{})
	if len(oneOf) != 2 {
		t.Fatalf("统一会话令牌交付分支数=%d want 2", len(oneOf))
	}
	branchFields := make(map[string]struct{}, 2)
	for _, rawBranch := range oneOf {
		branch, _ := rawBranch.(map[interface{}]interface{})
		branchRequired, _ := branch["required"].([]interface{})
		if len(branchRequired) != 1 {
			t.Fatalf("会话令牌交付分支必填字段=%v want exactly one", branchRequired)
		}
		field, _ := branchRequired[0].(string)
		branchFields[field] = struct{}{}
	}
	for _, field := range []string{"refresh_token", "csrf_token"} {
		if _, ok := branchFields[field]; !ok {
			t.Errorf("统一会话令牌合同缺少 %s 交付分支", field)
		}
	}
}

func TestProviderAccountRecoveryDeclaresPartialCommitContract(t *testing.T) {
	document := loadSpecDocument(t, filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml"))
	for _, path := range []string{
		"/admin/v1/provider-accounts/{id}/clear-rate-limit",
		"/admin/v1/provider-accounts/{id}/recover",
	} {
		operation := mustOperation(t, document, path, "post")
		responses, _ := operation["responses"].(map[interface{}]interface{})
		response503, _ := responses["503"].(map[interface{}]interface{})
		content, _ := response503["content"].(map[interface{}]interface{})
		jsonContent, _ := content["application/json"].(map[interface{}]interface{})
		schema, _ := jsonContent["schema"].(map[interface{}]interface{})
		oneOf, _ := schema["oneOf"].([]interface{})
		if !containsSchemaReference(oneOf, "#/components/schemas/ProviderAccountRecoveryPartial") {
			t.Fatalf("%s 的 503 未声明账号阶段已提交、渠道阶段待恢复的结构化合同", path)
		}
	}

	rawSchema, ok := resolveLocalReference(document, "#/components/schemas/ProviderAccountRecoveryPartial")
	if !ok {
		t.Fatal("OpenAPI 缺少 ProviderAccountRecoveryPartial")
	}
	schema, _ := rawSchema.(map[interface{}]interface{})
	required, _ := schema["required"].([]interface{})
	for _, field := range []string{
		"account_id",
		"operation",
		"account_backoff_cleared",
		"account_state_recovered",
		"channel_recovery_pending",
		"retryable",
	} {
		if !containsString(required, field) {
			t.Fatalf("部分恢复合同缺少必填字段 %s", field)
		}
	}
}

func TestProviderAccountOperationsDeclareRuntimeTenantScope(t *testing.T) {
	document := loadSpecDocument(t, filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml"))
	operations := []struct {
		path   string
		method string
	}{
		{"/admin/v1/provider-accounts", "get"},
		{"/admin/v1/provider-accounts", "post"},
		{"/admin/v1/provider-accounts/{id}", "get"},
		{"/admin/v1/provider-accounts/{id}", "patch"},
		{"/admin/v1/provider-accounts/{id}", "delete"},
		{"/admin/v1/provider-accounts/{id}/clear-rate-limit", "post"},
		{"/admin/v1/provider-accounts/{id}/recover", "post"},
		{"/admin/v1/provider-accounts/{id}/health", "get"},
		{"/admin/v1/provider-accounts/{id}/enabled", "patch"},
		{"/admin/v1/provider-accounts/{id}/fingerprint-profile", "patch"},
		{"/admin/v1/provider-accounts/{id}/upstream-models", "get"},
		{"/admin/v1/provider-accounts/{id}/upstream-models/sync", "post"},
		{"/v1/admin/provider-accounts/{id}/upstream-models", "get"},
		{"/v1/admin/provider-accounts/{id}/upstream-models/sync", "post"},
	}
	for _, item := range operations {
		if !operationDeclaresParameter(document, item.path, item.method, "tenant_id", "query") {
			t.Errorf("%s %s 缺少运行时 tenant_id 查询合同", strings.ToUpper(item.method), item.path)
		}
	}
}

func TestMissingOperationPathParametersChecksEachMethod(t *testing.T) {
	document := parseSpecDocument(t, `
openapi: 3.1.0
paths:
  /v1/items/{id}:
    get:
      parameters:
        - $ref: "#/components/parameters/ResourceId"
      responses:
        "200": { description: ok }
    patch:
      responses:
        "200": { description: ok }
components:
  parameters:
    ResourceId:
      name: id
      in: path
      required: true
      schema: { type: integer }
`)
	got := missingOperationPathParameters(document)
	want := []string{"PATCH /v1/items/{id}: id"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("缺失路径参数=%v，期望 %v", got, want)
	}
}

func loadSpecDocument(t *testing.T, path string) map[interface{}]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	return parseSpecDocument(t, string(raw))
}

func parseSpecDocument(t *testing.T, raw string) map[interface{}]interface{} {
	t.Helper()
	var document map[interface{}]interface{}
	if err := yaml.UnmarshalStrict([]byte(raw), &document); err != nil {
		t.Fatalf("解析 OpenAPI: %v", err)
	}
	return document
}

func unresolvedLocalReferences(document map[interface{}]interface{}) []string {
	refs := make(map[string]struct{})
	collectLocalReferences(document, refs)
	var unresolved []string
	for ref := range refs {
		if _, ok := resolveLocalReference(document, ref); !ok {
			unresolved = append(unresolved, ref)
		}
	}
	sort.Strings(unresolved)
	return unresolved
}

func collectLocalReferences(node any, refs map[string]struct{}) {
	switch value := node.(type) {
	case map[interface{}]interface{}:
		for key, child := range value {
			if key == "$ref" {
				if ref, ok := child.(string); ok && strings.HasPrefix(ref, "#/") {
					refs[ref] = struct{}{}
				}
			}
			collectLocalReferences(child, refs)
		}
	case []interface{}:
		for _, child := range value {
			collectLocalReferences(child, refs)
		}
	}
}

func containsSchemaReference(items []interface{}, want string) bool {
	for _, item := range items {
		schema, _ := item.(map[interface{}]interface{})
		if ref, _ := schema["$ref"].(string); ref == want {
			return true
		}
	}
	return false
}

func containsString(items []interface{}, want string) bool {
	for _, item := range items {
		if value, _ := item.(string); value == want {
			return true
		}
	}
	return false
}

func successfulResponseReferences(
	document map[interface{}]interface{},
	operation map[interface{}]interface{},
	target string,
) bool {
	responses, _ := operation["responses"].(map[interface{}]interface{})
	for rawStatus, rawResponse := range responses {
		status, _ := rawStatus.(string)
		if len(status) != 3 || status[0] != '2' {
			continue
		}
		if nodeReferences(document, rawResponse, target, make(map[string]struct{})) {
			return true
		}
	}
	return false
}

func nodeReferences(
	document map[interface{}]interface{},
	node any,
	target string,
	visited map[string]struct{},
) bool {
	switch value := node.(type) {
	case map[interface{}]interface{}:
		if ref, ok := value["$ref"].(string); ok {
			if ref == target {
				return true
			}
			if strings.HasPrefix(ref, "#/") {
				if _, seen := visited[ref]; seen {
					return false
				}
				visited[ref] = struct{}{}
				resolved, exists := resolveLocalReference(document, ref)
				if exists && nodeReferences(document, resolved, target, visited) {
					return true
				}
			}
		}
		for key, child := range value {
			if key == "$ref" {
				continue
			}
			if nodeReferences(document, child, target, visited) {
				return true
			}
		}
	case []interface{}:
		for _, child := range value {
			if nodeReferences(document, child, target, visited) {
				return true
			}
		}
	}
	return false
}

func resolveLocalReference(document map[interface{}]interface{}, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = document
	for _, rawPart := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(rawPart, "~1", "/"), "~0", "~")
		object, ok := current.(map[interface{}]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func missingOperationPathParameters(document map[interface{}]interface{}) []string {
	paths, ok := document["paths"].(map[interface{}]interface{})
	if !ok {
		return []string{"OpenAPI 缺少 paths 对象"}
	}
	methods := map[string]struct{}{
		"get": {}, "put": {}, "post": {}, "delete": {}, "options": {}, "head": {}, "patch": {}, "trace": {},
	}
	var missing []string
	for rawPath, rawItem := range paths {
		path, ok := rawPath.(string)
		if !ok {
			continue
		}
		requiredNames := pathParameterNames(path)
		if len(requiredNames) == 0 {
			continue
		}
		item, ok := rawItem.(map[interface{}]interface{})
		if !ok {
			continue
		}
		pathLevel := declaredPathParameters(document, item["parameters"])
		for rawMethod, rawOperation := range item {
			method, ok := rawMethod.(string)
			if !ok {
				continue
			}
			method = strings.ToLower(method)
			if _, ok := methods[method]; !ok {
				continue
			}
			operation, ok := rawOperation.(map[interface{}]interface{})
			if !ok {
				continue
			}
			declared := make(map[string]struct{}, len(pathLevel))
			for name := range pathLevel {
				declared[name] = struct{}{}
			}
			for name := range declaredPathParameters(document, operation["parameters"]) {
				declared[name] = struct{}{}
			}
			var absent []string
			for _, name := range requiredNames {
				if _, ok := declared[name]; !ok {
					absent = append(absent, name)
				}
			}
			if len(absent) != 0 {
				missing = append(missing, fmt.Sprintf("%s %s: %s", strings.ToUpper(method), path, strings.Join(absent, ", ")))
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func declaredPathParameters(document map[interface{}]interface{}, raw any) map[string]struct{} {
	out := make(map[string]struct{})
	items, ok := raw.([]interface{})
	if !ok {
		return out
	}
	for _, item := range items {
		parameter, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		if ref, ok := parameter["$ref"].(string); ok {
			resolved, exists := resolveLocalReference(document, ref)
			if !exists {
				continue
			}
			parameter, ok = resolved.(map[interface{}]interface{})
			if !ok {
				continue
			}
		}
		name, _ := parameter["name"].(string)
		location, _ := parameter["in"].(string)
		required, _ := parameter["required"].(bool)
		if name != "" && location == "path" && required {
			out[name] = struct{}{}
		}
	}
	return out
}

func pathParameterNames(path string) []string {
	matches := pathTemplateVariable.FindAllStringSubmatch(path, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			out = append(out, match[1])
		}
	}
	return out
}

func mustOperation(t *testing.T, document map[interface{}]interface{}, path, method string) map[interface{}]interface{} {
	t.Helper()
	paths, ok := document["paths"].(map[interface{}]interface{})
	if !ok {
		t.Fatal("OpenAPI 缺少 paths 对象")
	}
	item, ok := paths[path].(map[interface{}]interface{})
	if !ok {
		t.Fatalf("OpenAPI 缺少路径 %s", path)
	}
	operation, ok := item[method].(map[interface{}]interface{})
	if !ok {
		t.Fatalf("OpenAPI 缺少操作 %s %s", strings.ToUpper(method), path)
	}
	return operation
}

func operationDeclaresParameter(document map[interface{}]interface{}, path, method, name, location string) bool {
	paths, ok := document["paths"].(map[interface{}]interface{})
	if !ok {
		return false
	}
	item, ok := paths[path].(map[interface{}]interface{})
	if !ok {
		return false
	}
	operation, ok := item[method].(map[interface{}]interface{})
	if !ok {
		return false
	}
	return parameterListDeclares(document, item["parameters"], name, location) ||
		parameterListDeclares(document, operation["parameters"], name, location)
}

func parameterListDeclares(document map[interface{}]interface{}, raw any, name, location string) bool {
	items, ok := raw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range items {
		parameter, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		if ref, ok := parameter["$ref"].(string); ok {
			resolved, exists := resolveLocalReference(document, ref)
			if !exists {
				continue
			}
			parameter, ok = resolved.(map[interface{}]interface{})
			if !ok {
				continue
			}
		}
		if parameter["name"] == name && parameter["in"] == location {
			return true
		}
	}
	return false
}
