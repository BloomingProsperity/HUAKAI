package modelhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/shopspring/decimal"
)

type authStub struct {
	ident auth.Identity
	err   error
	calls int
}

func (s *authStub) Resolve(_ context.Context, _ *http.Request) (auth.Identity, error) {
	s.calls++
	return s.ident, s.err
}

type catalogStub struct {
	models   []registry.ListedModel
	err      error
	tenantID int64
	calls    int
}

func (s *catalogStub) ListModels(_ context.Context, tenantID int64) ([]registry.ListedModel, error) {
	s.calls++
	s.tenantID = tenantID
	return s.models, s.err
}

type pricerStub struct {
	table    billing.PublicPriceTable
	err      error
	tenantID int64
	calls    int
}

func (s *pricerStub) PublicModelPrices(_ context.Context, tenantID int64) (billing.PublicPriceTable, error) {
	s.calls++
	s.tenantID = tenantID
	return s.table, s.err
}

func TestListModelsReturnsOpenAIModelObjectsForAuthenticatedTenant(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	authn := &authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 23}}
	catalog := &catalogStub{models: []registry.ListedModel{
		{ID: "gpt-4o", CreatedAt: created, OwnedBy: "openai"},
		{ID: "claude-sonnet", CreatedAt: created.Add(time.Hour), OwnedBy: "anthropic"},
	}}
	handler := NewListHandler(Deps{Auth: authn, Catalog: catalog})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if catalog.calls != 1 || catalog.tenantID != 7 {
		t.Fatalf("catalog calls=%d tenant=%d want one call for tenant 7", catalog.calls, catalog.tenantID)
	}
	var got struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Object != "list" || len(got.Data) != 2 {
		t.Fatalf("response=%+v want list with two models", got)
	}
	if got.Data[0].ID != "gpt-4o" || got.Data[0].Object != "model" || got.Data[0].Created != 1704067200 || got.Data[0].OwnedBy != "openai" {
		t.Fatalf("first model=%+v want OpenAI model object for gpt-4o", got.Data[0])
	}
	if got.Data[1].ID != "claude-sonnet" || got.Data[1].OwnedBy != "anthropic" {
		t.Fatalf("second model=%+v want catalog order and owner preserved", got.Data[1])
	}
}

func TestListModelsEnrichesPublicPricingAndContextLengthWhenAvailable(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	authn := &authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 23}}
	catalog := &catalogStub{models: []registry.ListedModel{
		{
			ID:            "Claude Opus Public",
			CanonicalID:   "anthropic/claude-opus-4",
			ContextWindow: 200000,
			CreatedAt:     created,
			OwnedBy:       "anthropic",
		},
		{
			ID:            "mystery",
			CanonicalID:   "mystery-x",
			ContextWindow: 0,
			CreatedAt:     created.Add(time.Hour),
			OwnedBy:       "HUAKAI",
		},
	}}
	pricer := &pricerStub{table: billing.NewPublicPriceTable("public-v1", map[string]billing.PublicPrice{
		"anthropic/claude-opus-4": {
			InputPerToken:  decimal.RequireFromString("0.0000004"),
			OutputPerToken: decimal.RequireFromString("0.0000016"),
			HasInput:       true,
			HasOutput:      true,
		},
	})}
	handler := NewListHandler(Deps{Auth: authn, Catalog: catalog, Pricing: pricer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if pricer.calls != 1 || pricer.tenantID != 7 {
		t.Fatalf("pricing calls=%d tenant=%d want one call for tenant 7", pricer.calls, pricer.tenantID)
	}
	var got struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Object != "list" || len(got.Data) != 2 {
		t.Fatalf("response=%+v want list with two models", got)
	}

	var priced struct {
		ID            string `json:"id"`
		Object        string `json:"object"`
		Created       int64  `json:"created"`
		OwnedBy       string `json:"owned_by"`
		ContextLength *int   `json:"context_length,omitempty"`
		Pricing       *struct {
			InputPerToken  string `json:"input_per_token"`
			OutputPerToken string `json:"output_per_token"`
		} `json:"pricing,omitempty"`
	}
	if err := json.Unmarshal(got.Data[0], &priced); err != nil {
		t.Fatalf("decode priced model: %v raw=%s", err, string(got.Data[0]))
	}
	if priced.ID != "Claude Opus Public" || priced.Object != "model" || priced.Created != 1704067200 || priced.OwnedBy != "anthropic" {
		t.Fatalf("priced model base fields=%+v want existing OpenAI-compatible fields preserved", priced)
	}
	if priced.ContextLength == nil || *priced.ContextLength != 200000 {
		t.Fatalf("context_length=%v want 200000", priced.ContextLength)
	}
	if priced.Pricing == nil {
		t.Fatalf("pricing missing for canonical-id priced model raw=%s", string(got.Data[0]))
	}
	if priced.Pricing.InputPerToken != "0.0000004" || priced.Pricing.OutputPerToken != "0.0000016" {
		t.Fatalf("pricing=%+v want per-token USD strings", priced.Pricing)
	}

	var unpriced map[string]any
	if err := json.Unmarshal(got.Data[1], &unpriced); err != nil {
		t.Fatalf("decode unpriced model: %v raw=%s", err, string(got.Data[1]))
	}
	if unpriced["id"] != "mystery" || unpriced["owned_by"] != "HUAKAI" {
		t.Fatalf("unpriced model=%+v want base fields retained", unpriced)
	}
	if _, ok := unpriced["pricing"]; ok {
		t.Fatalf("unpriced model raw=%s unexpectedly includes pricing", string(got.Data[1]))
	}
	if _, ok := unpriced["context_length"]; ok {
		t.Fatalf("unpriced model raw=%s unexpectedly includes context_length", string(got.Data[1]))
	}

	lowerBody := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"multiplier", "cache", "micro", "cost", "markup"} {
		if strings.Contains(lowerBody, forbidden) {
			t.Fatalf("response body leaks %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestListModelsEnrichesCapabilitiesMaxOutputAndModeWhenAvailable(t *testing.T) {
	created := time.Unix(1704067200, 0).UTC()
	maxOutput := 8192
	authn := &authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 23}}
	catalog := &catalogStub{models: []registry.ListedModel{
		{
			ID:              "gpt-rich",
			CanonicalID:     "openai/gpt-rich",
			CreatedAt:       created,
			OwnedBy:         "openai",
			Capabilities:    map[string]bool{"function_calling": true, "tool_choice": true, "vision": true},
			MaxOutputTokens: &maxOutput,
			Mode:            "chat",
		},
		{
			ID:          "plain",
			CanonicalID: "openai/plain",
			CreatedAt:   created.Add(time.Hour),
			OwnedBy:     "HUAKAI",
		},
	}}
	handler := NewListHandler(Deps{Auth: authn, Catalog: catalog})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var got struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Object != "list" || len(got.Data) != 2 {
		t.Fatalf("response=%+v want list with two models", got)
	}

	var rich struct {
		ID              string          `json:"id"`
		Capabilities    map[string]bool `json:"capabilities,omitempty"`
		MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
		Mode            string          `json:"mode,omitempty"`
	}
	if err := json.Unmarshal(got.Data[0], &rich); err != nil {
		t.Fatalf("decode rich model: %v raw=%s", err, string(got.Data[0]))
	}
	if rich.ID != "gpt-rich" || !rich.Capabilities["vision"] || !rich.Capabilities["function_calling"] || !rich.Capabilities["tool_choice"] {
		t.Fatalf("rich capabilities=%+v raw=%s", rich.Capabilities, string(got.Data[0]))
	}
	if rich.MaxOutputTokens == nil || *rich.MaxOutputTokens != 8192 {
		t.Fatalf("max_output_tokens=%v want 8192", rich.MaxOutputTokens)
	}
	if rich.Mode != "chat" {
		t.Fatalf("mode=%q want chat", rich.Mode)
	}

	var plain map[string]any
	if err := json.Unmarshal(got.Data[1], &plain); err != nil {
		t.Fatalf("decode plain model: %v raw=%s", err, string(got.Data[1]))
	}
	for _, forbidden := range []string{"capabilities", "max_output_tokens", "mode"} {
		if _, ok := plain[forbidden]; ok {
			t.Fatalf("plain model raw=%s unexpectedly includes %s", string(got.Data[1]), forbidden)
		}
	}
}

func TestListModelsAuthFailuresUseGatewayErrorContract(t *testing.T) {
	catalog := &catalogStub{}
	handler := NewListHandler(Deps{
		Auth:    &authStub{err: auth.ErrUnauthorized},
		Catalog: catalog,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if catalog.calls != 0 {
		t.Fatalf("catalog calls=%d want 0 when auth fails", catalog.calls)
	}
	assertErrorCode(t, rec.Body.Bytes(), "unauthorized")
}

func TestListModelsRegistryBackendErrorIsServiceUnavailable(t *testing.T) {
	handler := NewListHandler(Deps{
		Auth:    &authStub{ident: auth.Identity{TenantID: 7}},
		Catalog: &catalogStub{err: registry.ErrRegistryBackend},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.Bytes(), "registry_backend_error")
}

func TestListModelsUnexpectedCatalogErrorIsInternalError(t *testing.T) {
	handler := NewListHandler(Deps{
		Auth:    &authStub{ident: auth.Identity{TenantID: 7}},
		Catalog: &catalogStub{err: errors.New("boom")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s want 500", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.Bytes(), "registry_unknown_error")
}

func assertErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, string(body))
	}
	if got.Error.Code != want {
		t.Fatalf("error code=%q want %q body=%s", got.Error.Code, want, string(body))
	}
}
