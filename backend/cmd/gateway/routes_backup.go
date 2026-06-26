package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/backuphttp"
)

// mountBackupRoutes 挂只读备份 manifest 端点(GET /v1/admin/backup/manifest),adminGate 强制
// platform_admin(全库元数据属平台级)。纯只读 pg_catalog,零业务数据导出、零写入、零凭据外泄。
// pgPool 未配时 store 为 nil → handler 返回 503(fail-closed)。仿 routes_systemhealth.go 范式。
func mountBackupRoutes(r chi.Router, d *deps) {
	if d == nil {
		return
	}
	var resolver adminIdentityResolver
	if d.adminAuth != nil {
		resolver = d.adminAuth
	}
	var store backuphttp.Store
	if d.pgPool != nil {
		store = backuphttp.NewPostgresStore(d.pgPool)
	}
	h := backuphttp.NewManifestHandler(store)
	r.Method(http.MethodGet, "/v1/admin/backup/manifest", adminGate(resolver, h))
}
