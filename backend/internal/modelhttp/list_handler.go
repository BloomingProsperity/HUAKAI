package modelhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type authResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type modelCatalog interface {
	ListModels(context.Context, int64) ([]registry.ListedModel, error)
}

type modelPricer interface {
	PublicModelPrices(context.Context, int64) (billing.PublicPriceTable, error)
}

type Deps struct {
	Auth    authResolver
	Catalog modelCatalog
	Pricing modelPricer
}

type listResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

type modelObject struct {
	ID              string          `json:"id"`
	Object          string          `json:"object"`
	Created         int64           `json:"created"`
	OwnedBy         string          `json:"owned_by"`
	ContextLength   *int            `json:"context_length,omitempty"`
	Capabilities    map[string]bool `json:"capabilities,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Pricing         *modelPricing   `json:"pricing,omitempty"`
}

type modelPricing struct {
	InputPerToken  string `json:"input_per_token,omitempty"`
	OutputPerToken string `json:"output_per_token,omitempty"`
}

func NewListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Catalog == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "models handler dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}

		models, err := d.Catalog.ListModels(r.Context(), ident.TenantID)
		if errors.Is(err, registry.ErrRegistryBackend) {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend transient failure")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry_unknown_error", "model registry failed")
			return
		}

		var prices billing.PublicPriceTable
		if d.Pricing != nil {
			if table, err := d.Pricing.PublicModelPrices(r.Context(), ident.TenantID); err == nil {
				prices = table
			}
		}

		out := listResponse{
			Object: "list",
			Data:   make([]modelObject, 0, len(models)),
		}
		for _, model := range models {
			item := modelObject{
				ID:      model.ID,
				Object:  "model",
				Created: model.CreatedAt.Unix(),
				OwnedBy: model.OwnedBy,
			}
			if model.ContextWindow > 0 {
				contextLength := model.ContextWindow
				item.ContextLength = &contextLength
			}
			if len(model.Capabilities) > 0 {
				item.Capabilities = model.Capabilities
			}
			if model.MaxOutputTokens != nil {
				item.MaxOutputTokens = model.MaxOutputTokens
			}
			if model.Mode != "" {
				item.Mode = model.Mode
			}
			if price, ok := prices.Lookup(model.ID, model.CanonicalID); ok {
				pricing := modelPricing{}
				if price.HasInput {
					pricing.InputPerToken = price.InputPerToken.String()
				}
				if price.HasOutput {
					pricing.OutputPerToken = price.OutputPerToken.String()
				}
				item.Pricing = &pricing
			}
			out.Data = append(out.Data, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
