package modelhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
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
