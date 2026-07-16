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
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
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

	upsertCalls   int
	upsertParams  registry.UpsertModelCapabilityBindingParams
	upsertErr     error
	upsertBinding registry.ModelCapabilityBinding
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

func (s *adminModelAliasStoreStub) UpsertModelCapabilityBinding(_ context.Context, params registry.UpsertModelCapabilityBindingParams) (registry.ModelCapabilityBinding, error) {
	s.upsertCalls++
	s.upsertParams = params
	if s.upsertErr != nil {
		return registry.ModelCapabilityBinding{}, s.upsertErr
	}
	return s.upsertBinding, nil
}

func invokeAdminCapabilityBindingUpsert(t *testing.T, deps AdminModelAliasesDeps, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Method(http.MethodPut, "/v1/admin/models/{id}/capability-bindings", NewAdminModelCapabilityBindingUpsertHandler(deps))
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 守 upsert 核心: model_id 取自 path(42), source 服务端【强制 operator】(不取 body), 其余字段如实透传到 store。
// 用 enabled=false + scope=tenant + tenant_id=7 做判别值。
// mutation: handler 不强制 source 写死 operator(留空/取 body)→ upsertParams.Source != "operator" → 红;
//
//	model_id 不取 path → != 42 → 红; enabled 读错 → != false → 红。
func TestAdminCapabilityBindingUpsertForcesOperatorSourceAndPathModelID(t *testing.T) {
	store := &adminModelAliasStoreStub{upsertBinding: registry.ModelCapabilityBinding{ModelID: 42, Scope: "tenant", Capability: "vision", Enabled: false, Source: "operator"}}
	rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
		`{"scope":"tenant","capability":"vision","tenant_id":7,"enabled":false,"capability_value":"high","capability_params":{"levels":["low","high"]}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	p := store.upsertParams
	if p.Source != "operator" {
		t.Fatalf("upsert Source=%q want operator (服务端强制, 防伪装 vendor-sync)", p.Source)
	}
	if p.ModelID != 42 {
		t.Fatalf("upsert ModelID=%d want 42 (取自 path)", p.ModelID)
	}
	if p.Scope != "tenant" || p.Capability != "vision" || p.TenantID != 7 || p.Enabled != false {
		t.Fatalf("upsert params not faithfully forwarded: %+v", p)
	}
	// 守 capability_value + capability_params 也如实透传(非仅 scope/capability/enabled)。
	// mutation: handler 把 CapabilityValue/CapabilityParams 漏传(置 nil)→ 这两断言红。
	if p.CapabilityValue == nil || *p.CapabilityValue != "high" {
		t.Fatalf("upsert CapabilityValue=%v want 'high' (forward)", p.CapabilityValue)
	}
	if string(p.CapabilityParams) != `{"levels":["low","high"]}` {
		t.Fatalf("upsert CapabilityParams=%s want {\"levels\":[\"low\",\"high\"]} (raw forward)", string(p.CapabilityParams))
	}
	if !strings.Contains(rec.Body.String(), `"capability":"vision"`) {
		t.Fatalf("response missing binding: %s", rec.Body.String())
	}
}

// 守不可经 body 伪装 provenance: body 携带 source(或其它未知字段)→ 400(DisallowUnknownFields), 不触达 store。
// 这是关键安全属性: source 只能服务端强制 operator, 运营写入不得伪装成 vendor-sync 来源。
// mutation: parseCapabilityBindingUpsertBody 去 DisallowUnknownFields / 给请求体加 source 字段 → 伪装被接受 → 红。
func TestAdminCapabilityBindingUpsertRejectsBodySource(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
		`{"scope":"global","capability":"vision","enabled":true,"source":"model-sync-anthropic"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 (body source 伪装被拒)", rec.Code, rec.Body.String())
	}
	if store.upsertCalls != 0 {
		t.Fatalf("body-source payload touched store: calls=%d (provenance 伪装必须在解码层拦下)", store.upsertCalls)
	}
}

// 守显式存在: 省略 enabled → 400, 不触达 store。防 upsert 省略 enabled 时按零值 false 静默把已有 enabled 绑定翻 disabled。
// mutation: 去掉 req.Enabled==nil 检查 → 以 false 调 store / *nil 解引用 panic → 非 400 / store 触达 → 红。
func TestAdminCapabilityBindingUpsertRequiresEnabled(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
		`{"scope":"global","capability":"vision"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 (enabled 必填)", rec.Code, rec.Body.String())
	}
	if store.upsertCalls != 0 {
		t.Fatalf("missing-enabled payload touched store: calls=%d", store.upsertCalls)
	}
}

// 守非数字 model_id → 400, 不触达 store。
func TestAdminCapabilityBindingUpsertInvalidModelID(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/nope/capability-bindings",
		`{"scope":"global","capability":"vision","enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if store.upsertCalls != 0 {
		t.Fatalf("invalid model id touched store: calls=%d", store.upsertCalls)
	}
}

// 守 capability_params 形态: 非 JSON object(标量/数组)→ 400, 不触达 store; 合法 object 放行(不误拒)。
// mutation: 去掉 parseCapabilityBindingUpsertBody 的 object 守卫 → 标量/数组被接受落库 → 红。
func TestAdminCapabilityBindingUpsertRejectsNonObjectParams(t *testing.T) {
	for _, params := range []string{`"not-an-object"`, `[1,2,3]`, `42`} {
		store := &adminModelAliasStoreStub{}
		rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
			`{"scope":"global","capability":"vision","enabled":true,"capability_params":`+params+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("capability_params=%s status=%d want 400 (非 object 拒)", params, rec.Code)
		}
		if store.upsertCalls != 0 {
			t.Fatalf("capability_params=%s touched store: calls=%d", params, store.upsertCalls)
		}
	}
	// 对照(防过度严格): 合法 object capability_params 放行。
	store := &adminModelAliasStoreStub{}
	rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
		`{"scope":"global","capability":"vision","enabled":true,"capability_params":{"k":"v"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("object capability_params status=%d want 200 (不误拒合法 object) body=%s", rec.Code, rec.Body.String())
	}
}

// 守 store 错误映射: ErrUnknownModel→404; ErrInvalidModelCapability(非法 scope/capability)→400; 其它(未映射)→503 default。
// mutation: writeModelAliasStoreError 对应分支落 default(503)→ 404/400 case 红; 反之 default 分支被改→ 503 case 红。
func TestAdminCapabilityBindingUpsertStoreErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown model", registry.ErrUnknownModel, http.StatusNotFound},
		{"invalid capability", registry.ErrInvalidModelCapability, http.StatusBadRequest},
		{"unmapped backend error", registry.ErrRegistryBackend, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		store := &adminModelAliasStoreStub{upsertErr: c.err}
		rec := invokeAdminCapabilityBindingUpsert(t, AdminModelAliasesDeps{Store: store}, "/v1/admin/models/42/capability-bindings",
			`{"scope":"global","capability":"vision","enabled":true}`)
		if rec.Code != c.want {
			t.Fatalf("%s: status=%d want %d body=%s", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
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
// (admin_token:<TokenID>,来自 AdminIdentity.AuditActor())。身份经 admin.IdentityToContext 注入(模拟 adminGate 放行后注入)。
// mutation: handler 不覆盖 actor(信任 body)→ store 收到 "victim" → 红。
func TestAdminAliasBulkImportActorFromIdentityNotBody(t *testing.T) {
	store := &adminModelAliasStoreStub{}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		strings.NewReader(`{"aliases":[{"model_id":7,"alias":"gpt-a","scope":"global"}],"actor":"victim","reason":"user note"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(admin.IdentityToContext(req.Context(), admintest.Platform(4242)))
	rec := invokeAdminModelAliasesRequest(t, AdminModelAliasesDeps{Store: store}, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	// actor 取自认证身份, 非 body 'victim'(防伪造)。
	if store.params.Actor != "admin_token:4242" {
		t.Fatalf("store params.Actor=%q, want admin_token:4242 (取自认证身份, 非 body 'victim')", store.params.Actor)
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
	req = req.WithContext(admin.IdentityToContext(req.Context(), admintest.Platform(4242)))
	rec := invokeAdminModelAliasesRequest(t, AdminModelAliasesDeps{Store: store}, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.params.Actor != "admin_token:4242" {
		t.Fatalf("CSV 分支 actor=%q, want admin_token:4242 (override 路径无关)", store.params.Actor)
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
