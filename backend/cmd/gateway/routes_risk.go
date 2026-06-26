package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/riskoverviewhttp"
)

// mountRiskAdminRoutes 挂只读风控总览端点(/admin/v1/risk/overview,admin 鉴权)。
// 纯聚合已有风控信号的 COUNT,零处置零写入。pgPool 未配时 Store 为 nil → handler 返回 503。
func mountRiskAdminRoutes(r chi.Router, d *deps) {
	var auth riskoverviewhttp.AdminAuth
	var store riskoverviewhttp.Store
	if d != nil {
		auth = d.adminAuth
		if d.pgPool != nil {
			store = riskoverviewhttp.NewPostgresStore(d.pgPool)
		}
	}
	riskoverviewhttp.MountAdminRoutes(r, riskoverviewhttp.AdminDeps{Auth: auth, Store: store})
}
