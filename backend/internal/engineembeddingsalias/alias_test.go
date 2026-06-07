package engineembeddingsalias

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestEnginesEmbeddingAliasInjectsPathModelWhenBodyOmitsModel(t *testing.T) {
	var seen struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	r := chi.NewRouter()
	r.Post("/engines/{model}/embeddings", NewHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&seen); err != nil {
			t.Fatalf("decode rewritten body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/engines/text-embed-3/embeddings",
		strings.NewReader(`{"input":"x"}`))

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want delegate 202", rec.Code, rec.Body.String())
	}
	if seen.Model != "text-embed-3" {
		t.Fatalf("model=%q want text-embed-3 injected from path", seen.Model)
	}
	if seen.Input != "x" {
		t.Fatalf("input=%q want original input preserved", seen.Input)
	}
}

func TestEnginesEmbeddingAliasPreservesExplicitBodyModel(t *testing.T) {
	var seen struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	r := chi.NewRouter()
	r.Post("/engines/{model}/embeddings", NewHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&seen); err != nil {
			t.Fatalf("decode rewritten body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/engines/path-model/embeddings",
		strings.NewReader(`{"model":"body-model","input":"x"}`))

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want delegate 202", rec.Code, rec.Body.String())
	}
	if seen.Model != "body-model" {
		t.Fatalf("model=%q want explicit body model preserved", seen.Model)
	}
}
