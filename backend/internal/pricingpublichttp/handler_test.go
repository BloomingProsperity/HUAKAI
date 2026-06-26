package pricingpublichttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type publicPricingCatalogStub struct {
	fixtures []publicPricingModelFixture
	err      error
	calls    int
	tenantID int64
}

type publicPricingModelFixture struct {
	model   registry.ListedModel
	enabled bool
}

func (s *publicPricingCatalogStub) ListModels(_ context.Context, tenantID int64) ([]registry.ListedModel, error) {
	s.calls++
	s.tenantID = tenantID
	if s.err != nil {
		return nil, s.err
	}
	out := make([]registry.ListedModel, 0, len(s.fixtures))
	for _, fixture := range s.fixtures {
		if fixture.enabled {
			out = append(out, fixture.model)
		}
	}
	return out, nil
}

type publicPricingPricerStub struct {
	table    billing.PublicPriceTable
	err      error
	calls    int
	tenantID int64
}

func (s *publicPricingPricerStub) PublicModelPrices(_ context.Context, tenantID int64) (billing.PublicPriceTable, error) {
	s.calls++
	s.tenantID = tenantID
	return s.table, s.err
}

// 变异:给 GET /v1/pricing/page 加入任何 auth resolver/gate,都会把
// 这个不带请求头的请求变成 401,使测试失败。
func TestPublicPricingNoAuth(t *testing.T) {
	catalog, pricer := publicPricingFixture()
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if catalog.calls != 1 || catalog.tenantID != publicPricingTenantID {
		t.Fatalf("catalog calls=%d tenant=%d want one public-scope call", catalog.calls, catalog.tenantID)
	}
	if pricer.calls != 1 || pricer.tenantID != publicPricingTenantID {
		t.Fatalf("pricing calls=%d tenant=%d want one public-scope call", pricer.calls, pricer.tenantID)
	}
	items := decodePricingItems(t, rec)
	if len(items) != 1 || items[0].Model != "gpt-4.1-mini" {
		t.Fatalf("items=%+v want public model price list", items)
	}
}

// 变异:投射任何 raw rate-table 字段(如 actual_cost)、caller 身份
// 字段(如 user_id/api_key_id)或 provider_account_id,都会把
// 该字段名带进响应体,使本测试失败。
func TestPublicPricingProjection_NoCostOrIdentity(t *testing.T) {
	catalog, pricer := publicPricingFixture()
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{
		"actual_cost",
		"user_id",
		"api_key_id",
		"provider_account_id",
		"internal_ratio",
		"model_multiplier",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response body leaks %q: %s", forbidden, rec.Body.String())
		}
	}
	items := decodePricingItems(t, rec)
	if len(items) != 1 {
		t.Fatalf("items=%+v want exactly one priced model", items)
	}
	item := items[0]
	if item.Model != "gpt-4.1-mini" || item.CanonicalID != "openai/gpt-4.1-mini" {
		t.Fatalf("identity fields=%+v want public model and canonical id", item)
	}
	if item.InputPricePerToken != "0.0000004" || item.OutputPricePerToken != "0.0000016" {
		t.Fatalf("unit prices=%+v want public per-token prices", item)
	}
	if item.ContextLength == nil || *item.ContextLength != 128000 {
		t.Fatalf("context_length=%v want 128000", item.ContextLength)
	}
}

// 变异:绕过 registry 的 enabled-model 投射,或追加 disabled 的 catalog
// 行,都会把 disabled-preview 带进响应体并导致失败。
func TestPublicPricingListsEnabledModels(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	catalog := &publicPricingCatalogStub{fixtures: []publicPricingModelFixture{
		{enabled: true, model: registry.ListedModel{
			ID:            "enabled-public",
			CanonicalID:   "provider/enabled-public",
			CreatedAt:     created,
			OwnedBy:       "provider",
			ContextWindow: 64000,
		}},
		{enabled: false, model: registry.ListedModel{
			ID:            "disabled-preview",
			CanonicalID:   "provider/disabled-preview",
			CreatedAt:     created,
			OwnedBy:       "provider",
			ContextWindow: 64000,
		}},
	}}
	pricer := &publicPricingPricerStub{table: billing.NewPublicPriceTable("public-v1", map[string]billing.PublicPrice{
		"provider/enabled-public": {
			InputPerToken:  decimal.RequireFromString("0.0000002"),
			OutputPerToken: decimal.RequireFromString("0.0000008"),
			HasInput:       true,
			HasOutput:      true,
		},
		"provider/disabled-preview": {
			InputPerToken:  decimal.RequireFromString("0.0000999"),
			OutputPerToken: decimal.RequireFromString("0.0001999"),
			HasInput:       true,
			HasOutput:      true,
		},
	})}
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "enabled-public") {
		t.Fatalf("body=%s want enabled model present", body)
	}
	if strings.Contains(body, "disabled-preview") {
		t.Fatalf("body=%s must not include disabled model", body)
	}
}

// 变异:把历史 rate-table 的 version 查询参数设为必填,会让这个
// 朴素的 /v1/pricing/page 请求返回 400,使测试失败。
func TestPublicPricingNoParamsRequired(t *testing.T) {
	catalog, pricer := publicPricingFixture()
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 without version query", rec.Code, rec.Body.String())
	}
	if got := req.URL.Query().Get("version"); got != "" {
		t.Fatalf("test request unexpectedly has version=%q", got)
	}
}

func TestPublicPricingRegistryBackendErrorIsServiceUnavailable(t *testing.T) {
	handler := NewHandler(Deps{
		Catalog: &publicPricingCatalogStub{err: registry.ErrRegistryBackend},
		Pricing: &publicPricingPricerStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	assertPublicPricingErrorCode(t, rec, "registry_backend_error")
}

func TestPublicPricingPricingErrorIsServiceUnavailable(t *testing.T) {
	catalog, pricer := publicPricingFixture()
	pricer.err = errors.New("pricing backend down")
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	assertPublicPricingErrorCode(t, rec, "pricing_backend_error")
}

type decodedPricingItem struct {
	Model               string          `json:"model"`
	CanonicalID         string          `json:"canonical_id"`
	OwnedBy             string          `json:"owned_by,omitempty"`
	Mode                string          `json:"mode,omitempty"`
	InputPricePerToken  string          `json:"input_price_per_token"`
	OutputPricePerToken string          `json:"output_price_per_token"`
	ContextLength       *int            `json:"context_length,omitempty"`
	MaxOutputTokens     *int            `json:"max_output_tokens,omitempty"`
	Capabilities        map[string]bool `json:"capabilities,omitempty"`
}

func publicPricingFixture() (*publicPricingCatalogStub, *publicPricingPricerStub) {
	created := time.Unix(1704067200, 0).UTC()
	catalog := &publicPricingCatalogStub{fixtures: []publicPricingModelFixture{
		{enabled: true, model: registry.ListedModel{
			ID:              "gpt-4.1-mini",
			CanonicalID:     "openai/gpt-4.1-mini",
			CreatedAt:       created,
			OwnedBy:         "openai",
			Mode:            "chat",
			ContextWindow:   128000,
			MaxOutputTokens: ptrInt(16384),
			Capabilities:    map[string]bool{"vision": true, "tools": true},
		}},
	}}
	pricer := &publicPricingPricerStub{table: billing.NewPublicPriceTable("public-v1", map[string]billing.PublicPrice{
		"openai/gpt-4.1-mini": {
			InputPerToken:  decimal.RequireFromString("0.0000004"),
			OutputPerToken: decimal.RequireFromString("0.0000016"),
			HasInput:       true,
			HasOutput:      true,
		},
	})}
	return catalog, pricer
}

func decodePricingItems(t *testing.T, rec *httptest.ResponseRecorder) []decodedPricingItem {
	t.Helper()
	var items []decodedPricingItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return items
}

func assertPublicPricingErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, rec.Body.String())
	}
	if got.Error.Code != want {
		t.Fatalf("error code=%q want %q body=%s", got.Error.Code, want, rec.Body.String())
	}
}

// 变异:在 handler.go 中去掉四个 catalog-metadata 投射中的任意一个
// (owned_by / mode / max_output_tokens / capabilities),会让对应的
// 响应字段停在零值,与下面 seed 的判别性 fixture 值不同,
// 因此下面每条断言都会因该缺陷而变红。
func TestPublicPricingProjectsCatalogMetadata(t *testing.T) {
	catalog, pricer := publicPricingFixture()
	handler := NewHandler(Deps{Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	items := decodePricingItems(t, rec)
	if len(items) != 1 {
		t.Fatalf("items=%+v want exactly one priced model", items)
	}
	item := items[0]
	if item.OwnedBy != "openai" {
		t.Fatalf("owned_by=%q want openai (projection dropped?)", item.OwnedBy)
	}
	if item.Mode != "chat" {
		t.Fatalf("mode=%q want chat (projection dropped?)", item.Mode)
	}
	if item.MaxOutputTokens == nil || *item.MaxOutputTokens != 16384 {
		t.Fatalf("max_output_tokens=%v want 16384 (projection dropped?)", item.MaxOutputTokens)
	}
	if !item.Capabilities["vision"] || !item.Capabilities["tools"] {
		t.Fatalf("capabilities=%v want vision+tools (projection dropped?)", item.Capabilities)
	}
}

func ptrInt(n int) *int { return &n }
