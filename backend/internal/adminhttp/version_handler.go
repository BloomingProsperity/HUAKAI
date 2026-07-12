package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/buildinfo"
)

// VersionDeps 持有管理端 version 端点的认证依赖。
type VersionDeps struct {
	Auth versionAuth
}

type versionAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// MountVersionRoutes 在给定的 router 上注册 GET /version。
// 调用方应将其挂载到 /admin/v1 与 /v1/admin 下,以便同时服务两种
// 前缀约定。
func MountVersionRoutes(r chi.Router, d VersionDeps) {
	r.Get("/version", newVersionHandler(d))
}

func newVersionHandler(d VersionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "version dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		switch ident.Role {
		case admin.RoleTenantOperator, admin.RolePlatformAdmin:
			// 两种角色都可以读取构建信息
		default:
			writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
			return
		}
		snap := buildinfo.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(snap)
	}
}
