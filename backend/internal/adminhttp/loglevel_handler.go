package adminhttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

// LogLevelDeps holds the auth dependency for the admin log-level endpoint.
type LogLevelDeps struct {
	Auth logLevelAuth
}

type logLevelAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// MountLogLevelRoutes registers GET + PUT /loglevel. GET returns the current
// level; PUT {"level":"debug"} sets it at runtime (delegates to zap's
// AtomicLevel.ServeHTTP). Mount under /admin/v1 and /v1/admin.
func MountLogLevelRoutes(r chi.Router, d LogLevelDeps) {
	h := newLogLevelHandler(d)
	r.Get("/loglevel", h)
	r.Put("/loglevel", h)
}

func newLogLevelHandler(d LogLevelDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "loglevel dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// platform_admin only: changing global log verbosity is a platform op.
		if ident.Role != admin.RolePlatformAdmin {
			writeError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
			return
		}
		// zap's AtomicLevel serves GET (current level) and PUT ({"level":"debug"}).
		loglevel.Level.ServeHTTP(w, r)
	}
}
