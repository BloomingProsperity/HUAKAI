package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestAdminModelCapabilitiesPutPersistsPublicDescriptors(t *testing.T) {
	maxOutput := 8192
	store := &adminCapabilitiesStoreStub{
		row: registry.ModelCapabilityUpdate{
			ModelID:         42,
			Capabilities:    map[string]bool{"function_calling": true, "tool_choice": true, "vision": true},
			MaxOutputTokens: &maxOutput,
			ModelMode:       "chat",
		},
	}
	rec := invokeAdminCapabilities(t, AdminCapabilitiesDeps{Store: store},
		http.MethodPut, "/v1/admin/models/42/capabilities",
		`{"capabilities":{"vision":true,"function_calling":true,"tool_choice":true},"max_output_tokens":8192,"model_mode":" chat "}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
	if store.params.ModelID != 42 || store.params.ModelMode == nil || *store.params.ModelMode != "chat" {
		t.Fatalf("store params=%+v want model id 42 and trimmed chat mode", store.params)
	}
	if store.params.MaxOutputTokens == nil || *store.params.MaxOutputTokens != 8192 {
		t.Fatalf("store max_output_tokens=%v want 8192", store.params.MaxOutputTokens)
	}
	if !store.params.Capabilities["vision"] || !store.params.Capabilities["function_calling"] || !store.params.Capabilities["tool_choice"] {
		t.Fatalf("store capabilities=%+v want descriptor map", store.params.Capabilities)
	}

	var got struct {
		Object          string          `json:"object"`
		ID              int64           `json:"id"`
		Capabilities    map[string]bool `json:"capabilities"`
		MaxOutputTokens *int            `json:"max_output_tokens"`
		Mode            string          `json:"mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Object != "model_capabilities" || got.ID != 42 || got.MaxOutputTokens == nil || *got.MaxOutputTokens != 8192 || got.Mode != "chat" {
		t.Fatalf("response=%+v want persisted public descriptors", got)
	}
	if !got.Capabilities["vision"] || !got.Capabilities["function_calling"] || !got.Capabilities["tool_choice"] {
		t.Fatalf("response capabilities=%+v want descriptor map", got.Capabilities)
	}
}

func TestAdminModelCapabilitiesRejectsInvalidPayloadBeforeStore(t *testing.T) {
	store := &adminCapabilitiesStoreStub{}
	rec := invokeAdminCapabilities(t, AdminCapabilitiesDeps{Store: store},
		http.MethodPut, "/v1/admin/models/42/capabilities",
		`{"capabilities":{"vision":true},"max_output_tokens":0,"model_mode":"chat"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("invalid payload touched store: calls=%d", store.calls)
	}
	if !strings.Contains(rec.Body.String(), "invalid_max_output_tokens") {
		t.Fatalf("body=%s want invalid_max_output_tokens", rec.Body.String())
	}
}

func TestAdminModelCapabilitiesNotFoundMapsTo404(t *testing.T) {
	store := &adminCapabilitiesStoreStub{err: registry.ErrUnknownModel}
	rec := invokeAdminCapabilities(t, AdminCapabilitiesDeps{Store: store},
		http.MethodPut, "/v1/admin/models/42/capabilities",
		`{"capabilities":{"vision":true},"max_output_tokens":8192,"model_mode":"chat"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("body=%s want model_not_found", rec.Body.String())
	}
}

func TestAdminModelCapabilitiesBackendErrorMapsTo503(t *testing.T) {
	store := &adminCapabilitiesStoreStub{err: errors.New("db down")}
	rec := invokeAdminCapabilities(t, AdminCapabilitiesDeps{Store: store},
		http.MethodPut, "/v1/admin/models/42/capabilities",
		`{"capabilities":{"vision":true},"max_output_tokens":8192,"model_mode":"chat"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_capabilities_update_failed") {
		t.Fatalf("body=%s want model_capabilities_update_failed", rec.Body.String())
	}
}

type adminCapabilitiesStoreStub struct {
	row    registry.ModelCapabilityUpdate
	err    error
	calls  int
	params registry.UpdateModelCapabilitiesParams
}

func (s *adminCapabilitiesStoreStub) UpdateModelCapabilities(_ context.Context, params registry.UpdateModelCapabilitiesParams) (registry.ModelCapabilityUpdate, error) {
	s.calls++
	s.params = params
	if s.err != nil {
		return registry.ModelCapabilityUpdate{}, s.err
	}
	return s.row, nil
}

func invokeAdminCapabilities(t *testing.T, deps AdminCapabilitiesDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Put("/v1/admin/models/{id}/capabilities", NewAdminCapabilitiesHandler(deps))
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
