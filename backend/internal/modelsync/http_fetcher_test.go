package modelsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPFetcherParsesOpenAIModelListAndSendsBearer(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[{"id":"gpt-sync-new","object":"model","created":1761955200,"owned_by":"openai"}]
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorOpenAI,
		URL:     server.URL,
		APIKey:  "openai-secret",
		Client:  server.Client(),
		Timeout: time.Second,
	})

	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if sawAuth != "Bearer openai-secret" {
		t.Fatalf("Authorization=%q want bearer header", sawAuth)
	}
	if catalog.Vendor != VendorOpenAI || len(catalog.Models) != 1 {
		t.Fatalf("catalog=%+v want one OpenAI model", catalog)
	}
	model := catalog.Models[0]
	if model.ID != "gpt-sync-new" || model.OwnedBy != "openai" || model.ProtocolFamily != "openai_chat" {
		t.Fatalf("normalized model mismatch: %+v", model)
	}
	if !model.CreatedAt.Equal(time.Unix(1761955200, 0).UTC()) {
		t.Fatalf("CreatedAt=%s want unix timestamp mapped", model.CreatedAt)
	}
}

func TestHTTPFetcherFiltersOpenAINonChatModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[
				{"id":"gpt-4.1-mini","created":1761955200,"owned_by":"openai"},
				{"id":"text-embedding-3-small","created":1761955200,"owned_by":"openai"},
				{"id":"tts-1","created":1761955200,"owned_by":"openai"},
				{"id":"gpt-image-1","created":1761955200,"owned_by":"openai"}
			]
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorOpenAI,
		URL:     server.URL,
		Client:  server.Client(),
		Timeout: time.Second,
	})
	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].ID != "gpt-4.1-mini" {
		t.Fatalf("models=%+v want only chat-capable OpenAI id", catalog.Models)
	}
}

func TestHTTPFetcherParsesAnthropicModelListAndSendsVersionedHeader(t *testing.T) {
	var sawKey, sawVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("X-Api-Key")
		sawVersion = r.Header.Get("Anthropic-Version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[{"id":"claude-sync-new","display_name":"Claude Sync New","created_at":"2026-06-02T08:00:00Z","type":"model"}],
			"has_more":false
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorAnthropic,
		URL:     server.URL,
		APIKey:  "anthropic-secret",
		Client:  server.Client(),
		Timeout: time.Second,
	})

	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if sawKey != "anthropic-secret" || strings.TrimSpace(sawVersion) == "" {
		t.Fatalf("missing Anthropic auth/version headers: key=%q version=%q", sawKey, sawVersion)
	}
	if catalog.Vendor != VendorAnthropic || len(catalog.Models) != 1 {
		t.Fatalf("catalog=%+v want one Anthropic model", catalog)
	}
	model := catalog.Models[0]
	if model.ID != "claude-sync-new" || model.OwnedBy != "anthropic" || model.ProtocolFamily != "anthropic_messages" {
		t.Fatalf("normalized model mismatch: %+v", model)
	}
	if model.DisplayName != "Claude Sync New" {
		t.Fatalf("DisplayName=%q", model.DisplayName)
	}
}

func TestHTTPFetcherFailsAnthropicWhenPaginationCapWouldTruncateCatalog(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[{"id":"claude-page","display_name":"Claude Page"}],
			"has_more":true,
			"last_id":"claude-page"
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorAnthropic,
		URL:     server.URL,
		Client:  server.Client(),
		Timeout: time.Second,
	})
	_, err := fetcher.FetchCatalog(context.Background())
	if !errors.Is(err, ErrPaginationLimit) {
		t.Fatalf("err=%v want ErrPaginationLimit", err)
	}
	if requests != maxCatalogPages {
		t.Fatalf("requests=%d want page cap %d", requests, maxCatalogPages)
	}
}

func TestHTTPFetcherParsesGeminiModelListAndSendsAPIKey(t *testing.T) {
	var sawKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models":[{
				"name":"models/gemini-sync-new",
				"displayName":"Gemini Sync New",
				"description":"test model",
				"inputTokenLimit":1048576,
				"outputTokenLimit":65536,
				"supportedGenerationMethods":["generateContent","countTokens"]
			}]
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorGemini,
		URL:     server.URL,
		APIKey:  "gemini-secret",
		Client:  server.Client(),
		Timeout: time.Second,
	})

	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if sawKey != "gemini-secret" {
		t.Fatalf("Gemini query key=%q want configured key", sawKey)
	}
	if catalog.Vendor != VendorGemini || len(catalog.Models) != 1 {
		t.Fatalf("catalog=%+v want one Gemini model", catalog)
	}
	model := catalog.Models[0]
	if model.ID != "gemini-sync-new" || model.OwnedBy != "google" || model.ProtocolFamily != "gemini" {
		t.Fatalf("normalized model mismatch: %+v", model)
	}
	if model.ContextWindow != 1048576 {
		t.Fatalf("ContextWindow=%d want Gemini input token limit", model.ContextWindow)
	}
	if len(model.Capabilities) != 2 || model.Capabilities[0] != "generateContent" || model.Capabilities[1] != "countTokens" {
		t.Fatalf("Capabilities=%v want supported generation methods", model.Capabilities)
	}
}

func TestHTTPFetcherFailsGeminiWhenPaginationCapWouldTruncateCatalog(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models":[{"name":"models/gemini-page","supportedGenerationMethods":["generateContent"]}],
			"nextPageToken":"next"
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:  VendorGemini,
		URL:     server.URL,
		Client:  server.Client(),
		Timeout: time.Second,
	})
	_, err := fetcher.FetchCatalog(context.Background())
	if !errors.Is(err, ErrPaginationLimit) {
		t.Fatalf("err=%v want ErrPaginationLimit", err)
	}
	if requests != maxCatalogPages {
		t.Fatalf("requests=%d want page cap %d", requests, maxCatalogPages)
	}
}
