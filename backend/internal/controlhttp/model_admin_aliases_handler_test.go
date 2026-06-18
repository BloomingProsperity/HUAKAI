package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
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

// 守严格请求契约(JSON 分支): bulk-import body 携带未知顶层字段 → 400, 不触达 store。
// mutation: parseAliasBulkImportBody 去掉 DisallowUnknownFields → 未知字段静默丢弃 → store 触达 → 红。
func TestAdminModelAliasBulkImportRejectsUnknownFields(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminModelAliases(t, AdminModelAliasesDeps{Store: store},
		`{"aliases":[{"model_id":7,"alias":"gpt-a","scope":"global"}],"is_superuser":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 (unknown field rejected)", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("unknown-field payload touched store: calls=%d", store.calls)
	}
}

// 守审计归属不可伪造: body 设 actor:"victim" 也无效, 实际传给 store 的 params.Actor 取自认证身份
// (admin-token:<TokenID>)。身份经 admin.IdentityToContext 注入(模拟 adminGate 放行后注入)。
// mutation: handler 不覆盖 actor(信任 body)→ store 收到 "victim" → 红。
func TestAdminAliasBulkImportActorFromIdentityNotBody(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		strings.NewReader(`{"aliases":[{"model_id":7,"alias":"gpt-a","scope":"global"}],"actor":"victim","reason":"user note"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(admin.IdentityToContext(req.Context(), admin.AdminIdentity{TokenID: 4242, Role: admin.RolePlatformAdmin}))
	rec := invokeAdminModelAliasesRequest(t, AdminModelAliasesDeps{Store: store}, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	// actor 取自认证身份, 非 body 'victim'(防伪造)。
	if store.params.Actor != "admin-token:4242" {
		t.Fatalf("store params.Actor=%q, want admin-token:4242 (取自认证身份, 非 body 'victim')", store.params.Actor)
	}
	// 判别(Reason 保留不变量): Reason 是合法用户备注, 只覆盖 actor 不得连带清掉 reason。
	// mutation: handler 误覆盖/清空 params.Reason → 此断言红。
	if store.params.Reason != "user note" {
		t.Fatalf("store params.Reason=%q, want 'user note' (覆盖 actor 不应动 reason)", store.params.Reason)
	}
}

// 守 defensive: 无认证身份注入(异常/未经 gate)时绝不回退去信任 body actor → actor 置空。
// mutation: handler else 分支保留 body actor → store 收到 "victim" → 红。
func TestAdminAliasBulkImportActorEmptyWhenNoIdentity(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminModelAliases(t, AdminModelAliasesDeps{Store: store},
		`{"aliases":[{"model_id":7,"alias":"gpt-a","scope":"global"}],"actor":"victim"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if store.params.Actor != "" {
		t.Fatalf("no-identity actor=%q, want empty (绝不信任 body)", store.params.Actor)
	}
}

// 守 CSV 分支同样覆盖 actor: actor override 在 handler(解析之后)做, 与 JSON/CSV 格式无关。
// mutation: 把 override 误移进 JSON-only 解析路径 → CSV 分支不再覆盖 → CSV actor 仍是默认/空 → 红。
func TestAdminAliasBulkImportCSVActorFromIdentity(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		strings.NewReader("scope,model_id,alias,status\nglobal,7,gpt-a,active\n"))
	req.Header.Set("Content-Type", "text/csv")
	req = req.WithContext(admin.IdentityToContext(req.Context(), admin.AdminIdentity{TokenID: 4242, Role: admin.RolePlatformAdmin}))
	rec := invokeAdminModelAliasesRequest(t, AdminModelAliasesDeps{Store: store}, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.params.Actor != "admin-token:4242" {
		t.Fatalf("CSV 分支 actor=%q, want admin-token:4242 (override 路径无关)", store.params.Actor)
	}
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
