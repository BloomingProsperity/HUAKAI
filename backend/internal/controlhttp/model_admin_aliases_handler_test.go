package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestAdminAliasBulkImportJSONReportsEachRow(t *testing.T) {
	store := &adminModelAliasStoreStub{
		results: []registry.ModelAliasImportResult{
			{Index: 0, Alias: "gpt-4o", ModelID: 41, Status: "upserted"},
			{Index: 1, Alias: "claude-sonnet", ModelID: 42, Status: "upserted"},
		},
	}
	rec := invokeAdminModelAliases(t, AdminModelAliasesDeps{Store: store},
		`{"aliases":[{"tenant_id":7,"model_id":41,"alias":"gpt-4o"},{"tenant_id":7,"model_id":42,"alias":"claude-sonnet"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || len(store.params.Aliases) != 2 {
		t.Fatalf("store calls=%d aliases=%+v want two imported rows", store.calls, store.params.Aliases)
	}
	var got struct {
		Object  string                            `json:"object"`
		Results []registry.ModelAliasImportResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Object != "model_alias_bulk_import_result" || len(got.Results) != 2 {
		t.Fatalf("response=%+v want two per-row results", got)
	}
	if got.Results[1].Alias != "claude-sonnet" {
		t.Fatalf("second row alias=%q want claude-sonnet; MUTATION: importing only the first row must fail", got.Results[1].Alias)
	}
}

func TestAdminAliasBulkImportCSVReportsEachRow(t *testing.T) {
	store := &adminModelAliasStoreStub{
		results: []registry.ModelAliasImportResult{
			{Index: 0, Alias: "gpt-a", ModelID: 51, Status: "upserted"},
			{Index: 1, Alias: "gpt-b", ModelID: 52, Status: "upserted"},
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		strings.NewReader("tenant_id,model_id,alias,display,status\n7,51,gpt-a,GPT A,active\n7,52,gpt-b,GPT B,active\n"))
	req.Header.Set("Content-Type", "text/csv")
	rec := invokeAdminModelAliasesRequest(t, AdminModelAliasesDeps{Store: store}, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(store.params.Aliases) != 2 || store.params.Aliases[1].Alias != "gpt-b" {
		t.Fatalf("csv aliases=%+v want both rows", store.params.Aliases)
	}
}

func TestAdminCapabilityBindingsGetReturnsRows(t *testing.T) {
	store := &adminModelAliasStoreStub{
		bindings: []registry.ModelCapabilityBinding{
			{ModelID: 42, Capability: "vision", Enabled: true, Source: "operator"},
			{ModelID: 42, Capability: "tool_use", Enabled: false, Source: "operator"},
		},
	}
	rec := invokeAdminCapabilityBindings(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.bindingModelID != 42 {
		t.Fatalf("binding model id=%d want 42", store.bindingModelID)
	}
	if !strings.Contains(rec.Body.String(), `"capability":"vision"`) || !strings.Contains(rec.Body.String(), `"capability":"tool_use"`) {
		t.Fatalf("body=%s want listed capability bindings", rec.Body.String())
	}
}

func TestAdminCapabilityBindingsInvalidModelIDSkipsStore(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminCapabilityBindings(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/nope/capability-bindings")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if store.bindingCalls != 0 {
		t.Fatalf("invalid model id touched store calls=%d", store.bindingCalls)
	}
}

type adminModelAliasStoreStub struct {
	results        []registry.ModelAliasImportResult
	bindings       []registry.ModelCapabilityBinding
	err            error
	bindingErr     error
	calls          int
	bindingCalls   int
	bindingModelID int64
	params         registry.BulkImportModelAliasesParams
}

func (s *adminModelAliasStoreStub) BulkImportModelAliases(_ context.Context, params registry.BulkImportModelAliasesParams) ([]registry.ModelAliasImportResult, error) {
	s.calls++
	s.params = params
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

func (s *adminModelAliasStoreStub) ListModelCapabilityBindings(_ context.Context, modelID int64) ([]registry.ModelCapabilityBinding, error) {
	s.bindingCalls++
	s.bindingModelID = modelID
	if s.bindingErr != nil {
		return nil, s.bindingErr
	}
	return s.bindings, nil
}

func invokeAdminModelAliases(t *testing.T, deps AdminModelAliasesDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return invokeAdminModelAliasesRequest(t, deps, req)
}

func invokeAdminModelAliasesRequest(t *testing.T, deps AdminModelAliasesDeps, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/v1/admin/models/aliases/bulk-import", NewAdminModelAliasBulkImportHandler(deps))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func invokeAdminCapabilityBindings(t *testing.T, deps AdminModelAliasesDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/admin/models/{id}/capability-bindings", NewAdminModelCapabilityBindingsHandler(deps))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
