package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type AdminCapabilitiesDeps struct {
	Store adminCapabilitiesStore
}

type adminCapabilitiesStore interface {
	UpdateModelCapabilities(context.Context, registry.UpdateModelCapabilitiesParams) (registry.ModelCapabilityUpdate, error)
}

type capabilitiesRequestBody struct {
	Capabilities    map[string]bool `json:"capabilities"`
	MaxOutputTokens *int            `json:"max_output_tokens"`
	ModelMode       *string         `json:"model_mode"`
}

type capabilitiesResponseBody struct {
	Object          string          `json:"object"`
	ID              int64           `json:"id"`
	Capabilities    map[string]bool `json:"capabilities"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Mode            string          `json:"mode,omitempty"`
}

func NewAdminCapabilitiesHandler(d AdminCapabilitiesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model capabilities dependency unset")
			return
		}
		modelID, ok := parseModelIDParam(w, r)
		if !ok {
			return
		}
		body, ok := parseCapabilitiesBody(w, r)
		if !ok {
			return
		}

		row, err := d.Store.UpdateModelCapabilities(r.Context(), registry.UpdateModelCapabilitiesParams{
			ModelID:         modelID,
			Capabilities:    body.Capabilities,
			MaxOutputTokens: body.MaxOutputTokens,
			ModelMode:       body.ModelMode,
		})
		if err != nil {
			writeCapabilitiesStoreError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, capabilitiesResponse(row))
	}
}

func parseModelIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	modelID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || modelID <= 0 {
		modelWriteError(w, http.StatusBadRequest, "invalid_model_id", "model id must be a positive int64")
		return 0, false
	}
	return modelID, true
}

func parseCapabilitiesBody(w http.ResponseWriter, r *http.Request) (capabilitiesRequestBody, bool) {
	var body capabilitiesRequestBody
	if r.Body == nil {
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body required")
		return capabilitiesRequestBody{}, false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14))
	dec.DisallowUnknownFields() // 严格请求契约: 拒未知字段(防字段走私/契约漂移), 与 routeadmin/platformsettings 一致
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body required")
			return capabilitiesRequestBody{}, false
		}
		// 文案硬编码(对齐 routeadmin), 不回 err.Error() —— DisallowUnknownFields 拒未知字段时 err 含 "unknown field X",
		// 把请求 schema 字段名回给客户端是无谓的契约暴露。
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return capabilitiesRequestBody{}, false
	}
	if body.MaxOutputTokens != nil && *body.MaxOutputTokens <= 0 {
		modelWriteError(w, http.StatusBadRequest, "invalid_max_output_tokens", "max_output_tokens must be positive when set")
		return capabilitiesRequestBody{}, false
	}
	for key := range body.Capabilities {
		if strings.TrimSpace(key) == "" {
			modelWriteError(w, http.StatusBadRequest, "invalid_capabilities", "capability keys must be non-empty")
			return capabilitiesRequestBody{}, false
		}
	}
	if body.ModelMode != nil {
		mode := strings.TrimSpace(*body.ModelMode)
		body.ModelMode = &mode
	}
	return body, true
}

func capabilitiesResponse(row registry.ModelCapabilityUpdate) capabilitiesResponseBody {
	capabilities := row.Capabilities
	if capabilities == nil {
		capabilities = map[string]bool{}
	}
	return capabilitiesResponseBody{
		Object:          "model_capabilities",
		ID:              row.ModelID,
		Capabilities:    capabilities,
		MaxOutputTokens: row.MaxOutputTokens,
		Mode:            row.ModelMode,
	}
}

func writeCapabilitiesStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, registry.ErrUnknownModel) {
		modelWriteError(w, http.StatusNotFound, "model_not_found", "model not found")
		return
	}
	modelWriteError(w, http.StatusServiceUnavailable, "model_capabilities_update_failed", "model capabilities backend unavailable")
}
