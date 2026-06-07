package controlhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestGetModelReturnsMatchingOpenAIModelObject(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	authn := &modelListAuthStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 23}}
	catalog := &modelListCatalogStub{models: []registry.ListedModel{
		{ID: "first-model", CreatedAt: created.Add(-time.Hour), OwnedBy: "HUAKAI"},
		{ID: "gpt-x", CreatedAt: created, OwnedBy: "openai", ContextWindow: 128000},
	}}
	handler := NewModelGetHandler(ModelListDeps{Auth: authn, Catalog: catalog})
	r := chi.NewRouter()
	r.Get("/v1/models/{model}", handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-x", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if catalog.calls != 1 || catalog.tenantID != 7 {
		t.Fatalf("catalog calls=%d tenant=%d want one call for tenant 7", catalog.calls, catalog.tenantID)
	}
	var got struct {
		ID            string `json:"id"`
		Object        string `json:"object"`
		Created       int64  `json:"created"`
		OwnedBy       string `json:"owned_by"`
		ContextLength *int   `json:"context_length,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.ID != "gpt-x" || got.Object != "model" || got.Created != 1704067200 || got.OwnedBy != "openai" {
		t.Fatalf("model=%+v want gpt-x OpenAI-compatible object", got)
	}
	if got.ContextLength == nil || *got.ContextLength != 128000 {
		t.Fatalf("context_length=%v want 128000", got.ContextLength)
	}
}

func TestGetModelUnknownReturnsModelNotFound(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	authn := &modelListAuthStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 23}}
	catalog := &modelListCatalogStub{models: []registry.ListedModel{
		{ID: "gpt-x", CreatedAt: created, OwnedBy: "openai"},
	}}
	handler := NewModelGetHandler(ModelListDeps{Auth: authn, Catalog: catalog})
	r := chi.NewRouter()
	r.Get("/v1/models/{model}", handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/nope", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	assertModelListErrorCode(t, rec.Body.Bytes(), "model_not_found")
}
