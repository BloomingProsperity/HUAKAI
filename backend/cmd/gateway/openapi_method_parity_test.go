package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
)

// 审计修复钉死(method 维度;全局一致性测试只比 path 集合,抓不到 method 漂移):
//  1. /admin/v1/dlq/{handler} 列表 handler 是 r.Get(读 query 参数),spec 此前错写
//     post → codegen 客户端发 POST 实跑 chi 405,该管理端点经契约不可用。
//  2. DELETE /admin/v1/provider-accounts/{id}(软删)已实现且挂在 admin 鉴权后,
//     但 spec 此前缺该 operation → 前端无法 codegen 这个破坏性动作。
//
// Mutation guard: spec 改回 post / 删掉 delete operation → 对应断言红。
func TestOpenAPI_DLQListAndProviderAccountDeleteMethodParity(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	const dlqPath = "/admin/v1/dlq/{handler}"
	if !hasOperation(implOps, http.MethodGet, dlqPath) {
		t.Fatalf("runtime 缺 GET %s;routes.go 前提变了", dlqPath)
	}
	if !hasOperation(specOps, http.MethodGet, dlqPath) {
		t.Fatalf("OpenAPI 必须声明 GET %s(handler 为 r.Get,POST 实跑 405)", dlqPath)
	}
	if hasOperation(specOps, http.MethodPost, dlqPath) {
		t.Fatalf("OpenAPI 不得声明 POST %s;runtime 只挂 GET", dlqPath)
	}

	// runtime 探测用 /v1/admin 别名前缀:/admin/v1/provider-accounts 子树与
	// /admin/v1 Route 嵌套后 chi.Walk 不可见(运行时仍可路由),walker 只呈现
	// 别名挂载点;两个前缀共用同一 MountAdminPoolAccountRoutes。
	const paImplPath = "/v1/admin/provider-accounts/{id}"
	const paSpecPath = "/admin/v1/provider-accounts/{id}"
	if !hasOperation(implOps, http.MethodDelete, paImplPath) {
		t.Fatalf("runtime 缺 DELETE %s;MountAdminPoolAccountRoutes 前提变了", paImplPath)
	}
	if !hasOperation(specOps, http.MethodDelete, paSpecPath) {
		t.Fatalf("OpenAPI 必须声明 DELETE %s(软删已实现,缺契约前端无法 codegen)", paSpecPath)
	}
}

// TestOpenAPI_ProxyAdminMethodParity 钉死 /admin/v1/proxies 的 method 维度:
// 全局 path-集合一致性测试只比 path,抓不到「共享 path 上掉了一个 method」。
// 代理管理的 POST(建)/PATCH(改)/DELETE(软删)都与 GET 共享 path
// (/admin/v1/proxies 或 /{id}),所以删掉其一仍绿 → 必须逐 method 钉。
// Mutation guard: routes.go 去掉任一 method 或 spec 删对应 operation → 对应断言红。
func TestOpenAPI_ProxyAdminMethodParity(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	implOps := openapicheck.WalkChiOperations(buildTestRouter(t))

	// chi.Walk 把集合根(r.Get("/"))渲染成带尾斜杠的形式
	//(/admin/v1/proxies/),而 OpenAPI 的 path 不带斜杠;item 路径两侧一致。
	// hasOperation 是精确匹配,所以分别携带 impl + spec 路径
	//(与上面 provider-accounts 检查相同的模式)。
	const item = "/admin/v1/proxies/{id}"
	mutating := []struct {
		method, implPath, specPath string
	}{
		{http.MethodPost, "/admin/v1/proxies/", "/admin/v1/proxies"}, // create —— 与 GET list 共用路径
		{http.MethodPatch, item, item},                               // update —— 与 GET/DELETE 共用路径
		{http.MethodDelete, item, item},                              // soft-delete —— 破坏性,共用路径
	}
	for _, op := range mutating {
		if !hasOperation(implOps, op.method, op.implPath) {
			t.Fatalf("runtime 缺 %s %s;proxyadminhttp.MountRoutes 前提变了", op.method, op.implPath)
		}
		if !hasOperation(specOps, op.method, op.specPath) {
			t.Fatalf("OpenAPI 必须声明 %s %s(已实现,缺契约前端无法 codegen 这个变更动作)", op.method, op.specPath)
		}
	}
	const deleteImpactPath = "/admin/v1/proxies/{id}/delete-impact"
	if !hasOperation(implOps, http.MethodGet, deleteImpactPath) {
		t.Fatalf("runtime 缺 GET %s；前端无法在删除前展示占用影响", deleteImpactPath)
	}
	if !hasOperation(specOps, http.MethodGet, deleteImpactPath) {
		t.Fatalf("OpenAPI 必须声明 GET %s", deleteImpactPath)
	}

	const tenantDefaultPath = "/admin/v1/tenants/{id}/default-proxy"
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if !hasOperation(implOps, method, tenantDefaultPath) {
			t.Fatalf("runtime 缺 %s %s;租户默认出口未完整接线", method, tenantDefaultPath)
		}
		if !hasOperation(specOps, method, tenantDefaultPath) {
			t.Fatalf("OpenAPI 必须声明 %s %s", method, tenantDefaultPath)
		}
	}
}

func TestOpenAPI_ProviderAccountRecoveryActionMethodParity(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	implOps := openapicheck.WalkChiOperations(buildTestRouter(t))

	const runtimePath = "/v1/admin/provider-accounts/{id}/recovery-actions"
	if !hasOperation(implOps, http.MethodGet, runtimePath) {
		t.Fatalf("runtime 缺 GET %s；恢复动作诊断未接入共享 provider-account 子树", runtimePath)
	}
	for _, specPath := range []string{
		"/admin/v1/provider-accounts/{id}/recovery-actions",
		"/v1/admin/provider-accounts/{id}/recovery-actions",
	} {
		if !hasOperation(specOps, http.MethodGet, specPath) {
			t.Fatalf("OpenAPI 必须声明 GET %s", specPath)
		}
	}
}
