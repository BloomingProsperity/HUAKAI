package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsRouteMounted(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/models returned 404; route must be mounted for OpenAI-compatible model discovery")
	}
}
