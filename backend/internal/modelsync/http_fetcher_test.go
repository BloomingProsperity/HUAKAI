package modelsync

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
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
		Vendor:         VendorOpenAI,
		URL:            server.URL,
		APIKey:         "openai-secret",
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
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

// 回归:携带 vendor key 的 fetch 不得跟随上游 3xx 重定向,否则 key 会被泄漏
// 到重定向目标主机。Mutation:删掉 CheckRedirect → 默认 client 跟随 302 → leak
// 服务器被命中 → 本测试变红。
func TestHTTPFetcherRefusesRedirectToPreventKeyLeak(t *testing.T) {
	var leakHit int32
	leak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&leakHit, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/leaked"}]}`))
	}))
	defer leak.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, leak.URL+"/v1beta/models?stolen="+r.URL.Query().Get("key"), http.StatusFound)
	}))
	defer upstream.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:         VendorGemini,
		URL:            upstream.URL,
		APIKey:         "gemini-secret",
		Timeout:        time.Second,
		AllowUnsafeURL: true,
	})
	if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
		t.Fatalf("FetchCatalog: want error (3xx must not be followed), got nil")
	}
	if n := atomic.LoadInt32(&leakHit); n != 0 {
		t.Fatalf("leak server hit %d time(s): redirect was followed and key leaked", n)
	}
}

func TestHTTPFetcherRejectsUnsafeURLBeforeSendingKey(t *testing.T) {
	// 根因:model-sync 对 rawURL 无 scheme/host/IP 校验，旧代码会把 vendor key 发给
	// metadata/loopback/未知主机。Mutation:删掉 URL preflight 或 SSRF client 包装时，
	// RoundTripper 会被调用并捕获 Authorization/query key，本测试变红。
	cases := []struct {
		name       string
		vendor     Vendor
		rawURL     string
		wantSecret string
	}{
		{
			name:       "openai_metadata_ip",
			vendor:     VendorOpenAI,
			rawURL:     "https://169.254.169.254/v1/models",
			wantSecret: "Bearer openai-secret",
		},
		{
			name:       "gemini_loopback_ip",
			vendor:     VendorGemini,
			rawURL:     "https://127.0.0.1/v1beta/models",
			wantSecret: "gemini-secret",
		},
		{
			name:       "anthropic_unlisted_host",
			vendor:     VendorAnthropic,
			rawURL:     "https://models.attacker.test/v1/models",
			wantSecret: "anthropic-secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingModelSyncTransport{vendor: tc.vendor}
			fetcher := NewHTTPFetcher(HTTPFetcherConfig{
				Vendor:  tc.vendor,
				URL:     tc.rawURL,
				APIKey:  strings.TrimPrefix(tc.wantSecret, "Bearer "),
				Client:  &http.Client{Transport: transport},
				Timeout: time.Second,
			})

			if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
				t.Fatalf("FetchCatalog got nil error for unsafe URL %q", tc.rawURL)
			}
			if transport.calls != 0 {
				t.Fatalf("HTTP Do calls=%d want 0; leaked auth=%q query=%q", transport.calls, transport.auth, transport.queryKey)
			}
			if transport.auth == tc.wantSecret || transport.queryKey == tc.wantSecret {
				t.Fatalf("secret reached transport for unsafe URL: auth=%q query=%q", transport.auth, transport.queryKey)
			}
		})
	}
}

func TestHTTPFetcherClassifiesOpenAIModelsByOperation(t *testing.T) {
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
		Vendor:         VendorOpenAI,
		URL:            server.URL,
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
	})
	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if len(catalog.Models) != 4 {
		t.Fatalf("models=%+v，期望四种操作模型全部保留", catalog.Models)
	}
	want := map[string][]string{
		"gpt-4.1-mini":           {"chat", "responses"},
		"text-embedding-3-small": {"embeddings"},
		"tts-1":                  {"audio", "audio_speech"},
		"gpt-image-1":            {"image_output", "images"},
	}
	for _, model := range catalog.Models {
		if got, ok := want[model.ID]; !ok || !reflect.DeepEqual(model.Capabilities, got) {
			t.Fatalf("模型 %q 能力=%v，期望 %v", model.ID, model.Capabilities, got)
		}
	}
}

func TestHTTPFetcherClassifiesGrokImagesVideosAndResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer grok-key" {
			t.Fatalf("Authorization=%q，期望 Grok Bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"grok-4.20-multi-agent-0309","owned_by":"xai"},
			{"id":"grok-imagine-image-quality","owned_by":"xai"},
			{"id":"grok-imagine-video","owned_by":"xai"}
		]}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor: VendorGrok, URL: server.URL, APIKey: "grok-key",
		Client: server.Client(), Timeout: time.Second, AllowUnsafeURL: true,
	})
	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if catalog.Vendor != VendorGrok || len(catalog.Models) != 3 {
		t.Fatalf("Grok catalog=%+v", catalog)
	}
	want := map[string][]string{
		"grok-4.20-multi-agent-0309": {"responses", "tools"},
		"grok-imagine-image-quality": {"image_output", "images"},
		"grok-imagine-video":         {"video"},
	}
	for _, model := range catalog.Models {
		if model.ProtocolFamily != "grok_chat" || !reflect.DeepEqual(model.Capabilities, want[model.ID]) {
			t.Fatalf("Grok 模型分类错误: %+v", model)
		}
	}
}

func TestHTTPFetcherClassifiesGeminiMediaModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"name":"models/gemini-3.1-flash-image","supportedGenerationMethods":["generateContent","countTokens"]},
			{"name":"models/gemini-3.1-flash-tts-preview","supportedGenerationMethods":["generateContent"]},
			{"name":"models/veo-3.1-generate-preview","supportedGenerationMethods":["predictLongRunning"]}
		]}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor: VendorGemini, URL: server.URL, Client: server.Client(),
		Timeout: time.Second, AllowUnsafeURL: true,
	})
	catalog, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	want := map[string][]string{
		"gemini-3.1-flash-image":       {"generateContent", "countTokens", "image_output", "images"},
		"gemini-3.1-flash-tts-preview": {"generateContent", "audio", "audio_speech"},
		"veo-3.1-generate-preview":     {"video"},
	}
	for _, model := range catalog.Models {
		if !reflect.DeepEqual(model.Capabilities, want[model.ID]) {
			t.Fatalf("Gemini 模型 %q 能力=%v，期望 %v", model.ID, model.Capabilities, want[model.ID])
		}
	}
}

type recordingModelSyncTransport struct {
	vendor   Vendor
	calls    int
	auth     string
	queryKey string
}

func (t *recordingModelSyncTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	t.auth = req.Header.Get("Authorization")
	t.queryKey = req.URL.Query().Get("key")
	body := `{"data":[{"id":"gpt-unsafe","created":1761955200,"owned_by":"openai"}]}`
	switch t.vendor {
	case VendorAnthropic:
		body = `{"data":[{"id":"claude-unsafe","display_name":"Claude Unsafe"}],"has_more":false}`
	case VendorGemini:
		body = `{"models":[{"name":"models/gemini-unsafe","supportedGenerationMethods":["generateContent"]}]}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
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
		Vendor:         VendorAnthropic,
		URL:            server.URL,
		APIKey:         "anthropic-secret",
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
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
		Vendor:         VendorAnthropic,
		URL:            server.URL,
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
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
				"supportedGenerationMethods":["generateContent","predictLongRunning","countTokens","generateContent"," "]
			}]
		}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		Vendor:         VendorGemini,
		URL:            server.URL,
		APIKey:         "gemini-secret",
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
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
		t.Fatalf("Capabilities=%v want only normalized registry capabilities", model.Capabilities)
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
		Vendor:         VendorGemini,
		URL:            server.URL,
		Client:         server.Client(),
		Timeout:        time.Second,
		AllowUnsafeURL: true,
	})
	_, err := fetcher.FetchCatalog(context.Background())
	if !errors.Is(err, ErrPaginationLimit) {
		t.Fatalf("err=%v want ErrPaginationLimit", err)
	}
	if requests != maxCatalogPages {
		t.Fatalf("requests=%d want page cap %d", requests, maxCatalogPages)
	}
}
