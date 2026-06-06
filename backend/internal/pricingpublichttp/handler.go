package pricingpublichttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const publicPricingTenantID int64 = 0

type modelCatalog interface {
	ListModels(context.Context, int64) ([]registry.ListedModel, error)
}

type modelPricer interface {
	PublicModelPrices(context.Context, int64) (billing.PublicPriceTable, error)
}

type Deps struct {
	Catalog modelCatalog
	Pricing modelPricer
}

type pricingItem struct {
	Model               string `json:"model"`
	CanonicalID         string `json:"canonical_id,omitempty"`
	InputPricePerToken  string `json:"input_price_per_token,omitempty"`
	OutputPricePerToken string `json:"output_price_per_token,omitempty"`
	ContextLength       *int   `json:"context_length,omitempty"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Catalog == nil || d.Pricing == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "pricing page dependency unset")
			return
		}

		models, err := d.Catalog.ListModels(r.Context(), publicPricingTenantID)
		if errors.Is(err, registry.ErrRegistryBackend) {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend transient failure")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry_unknown_error", "model registry failed")
			return
		}

		prices, err := d.Pricing.PublicModelPrices(r.Context(), publicPricingTenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "pricing_backend_error", "public pricing backend unavailable")
			return
		}

		items := make([]pricingItem, 0, len(models))
		for _, model := range models {
			price, ok := prices.Lookup(model.ID, model.CanonicalID)
			if !ok {
				continue
			}
			item := pricingItem{
				Model:       model.ID,
				CanonicalID: model.CanonicalID,
			}
			if price.HasInput {
				item.InputPricePerToken = price.InputPerToken.String()
			}
			if price.HasOutput {
				item.OutputPricePerToken = price.OutputPerToken.String()
			}
			if model.ContextWindow > 0 {
				contextLength := model.ContextWindow
				item.ContextLength = &contextLength
			}
			items = append(items, item)
		}

		writeJSON(w, http.StatusOK, items)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
