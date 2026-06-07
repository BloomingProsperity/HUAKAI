package controlhttp

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func NewModelGetHandler(d ModelListDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Catalog == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "models handler dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			modelWriteError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			modelWriteError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			modelWriteError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}

		models, err := d.Catalog.ListModels(r.Context(), ident.TenantID)
		if errors.Is(err, registry.ErrRegistryBackend) {
			modelWriteError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend transient failure")
			return
		}
		if err != nil {
			modelWriteError(w, http.StatusInternalServerError, "registry_unknown_error", "model registry failed")
			return
		}

		var prices billing.PublicPriceTable
		if d.Pricing != nil {
			if table, err := d.Pricing.PublicModelPrices(r.Context(), ident.TenantID); err == nil {
				prices = table
			}
		}

		want := chi.URLParam(r, "model")
		for _, model := range models {
			if model.ID == want {
				modelWriteJSON(w, http.StatusOK, modelObjectFromListedModel(model, prices))
				return
			}
		}
		modelWriteError(w, http.StatusNotFound, "model_not_found", "model not found")
	}
}
