package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmode"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

type AdminAccountModesDeps struct {
	Auth    adminAccountModesAuth
	Catalog accountmode.Provider
}

type adminAccountModesAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

func NewAccountModeListHandler(d AdminAccountModesDeps) http.HandlerFunc {
	return newAccountModeListHandler(d)
}

func newAccountModeListHandler(d AdminAccountModesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin account mode dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
			writeAdminAuthError(w, admin.ErrAdminUnauthorized)
			return
		}

		provider := d.Catalog
		if provider == nil {
			provider = accountmode.DefaultProvider()
		}
		catalog, err := provider.Catalog(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "account_modes_unavailable",
				fmt.Sprintf("account mode catalog failed: %v", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(catalog)
	}
}
