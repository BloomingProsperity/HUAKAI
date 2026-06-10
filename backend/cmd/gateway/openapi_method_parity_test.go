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
