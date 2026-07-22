package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

// mountModuleRegistryRoutes 接线只读模块知识端点，并置于平台管理员权限门之后。
//
// GET /admin/v1/modules 返回合并后的身份、能力、状态和实时探测结果。
// GET /admin/v1/modules?category= 过滤到单一类别。
//
// 门控：复用与其它每个 /admin/v1/* 路由完全相同的 admin 鉴权 —— 由 d.adminAuth
// （*admin.AdminResolver）支撑的 adminGate(adminIdentityResolver, handler) 包装器，
// 与 mountSystemHealthRoutes 对 /admin/v1/system/health 的门控方式完全相同。
// 不引入任何新的鉴权。
func mountModuleRegistryRoutes(r chi.Router, d *deps) {
	if d == nil {
		return
	}
	var resolver adminIdentityResolver
	if d.adminAuth != nil {
		resolver = d.adminAuth
	}
	src := newModuleSource(d.moduleRegistry)
	h := modulehttp.NewModulesHandler(src)
	r.Method(http.MethodGet, "/admin/v1/modules", adminGate(resolver, h))
	r.Method(http.MethodGet, "/v1/admin/modules", adminGate(resolver, h))
}
