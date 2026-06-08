package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/buildinfo"
)

// VersionDeps holds the auth dependency for the admin version endpoint.
type VersionDeps struct {
	Auth versionAuth
}

type versionAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// MountVersionRoutes registers GET /version on the provided router.
// Callers should mount this under /admin/v1 and /v1/admin so both prefix
// conventions are served.
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
			// both roles may read build info
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
