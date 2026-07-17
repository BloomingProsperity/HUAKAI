package modeladminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type stubAuth struct {
	identity admin.AdminIdentity
	err      error
}

func (stub stubAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return stub.identity, stub.err
}

type stubService struct {
	models []registry.AdminModel
	model  registry.AdminModel
	err    error

	listCalled   bool
	getCalled    bool
	createCalled bool
	updateCalled bool
	deleteCalled bool

	lastAccess registry.AdminModelAccess
	lastTarget registry.AdminModelTarget
	lastID     int64
	lastCreate registry.CreateAdminModelInput
	lastUpdate registry.UpdateAdminModelInput
}

func (stub *stubService) ListAdminModels(_ context.Context, access registry.AdminModelAccess, target registry.AdminModelTarget) ([]registry.AdminModel, error) {
	stub.listCalled = true
	stub.lastAccess = access
	stub.lastTarget = target
	return stub.models, stub.err
}

func (stub *stubService) GetAdminModel(_ context.Context, access registry.AdminModelAccess, target registry.AdminModelTarget, id int64) (registry.AdminModel, error) {
	stub.getCalled = true
	stub.lastAccess = access
	stub.lastTarget = target
	stub.lastID = id
	return stub.model, stub.err
}

func (stub *stubService) CreateAdminModel(_ context.Context, input registry.CreateAdminModelInput) (registry.AdminModel, error) {
	stub.createCalled = true
	stub.lastAccess = input.Access
	stub.lastTarget = input.Target
	stub.lastCreate = input
	return stub.model, stub.err
}

func (stub *stubService) UpdateAdminModel(_ context.Context, input registry.UpdateAdminModelInput) (registry.AdminModel, error) {
	stub.updateCalled = true
	stub.lastAccess = input.Access
	stub.lastTarget = input.Target
	stub.lastUpdate = input
	return stub.model, stub.err
}

func (stub *stubService) SoftDeleteAdminModel(_ context.Context, access registry.AdminModelAccess, target registry.AdminModelTarget, id int64) error {
	stub.deleteCalled = true
	stub.lastAccess = access
	stub.lastTarget = target
	stub.lastID = id
	return stub.err
}

func platformIdentity(tokenID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: tokenID, Role: admin.RolePlatformAdmin}
}

func tenantIdentity(tokenID, tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: tokenID, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

func request(t *testing.T, identity admin.AdminIdentity, service *stubService, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	NewRouter(Deps{Auth: stubAuth{identity: identity}, Service: service}).ServeHTTP(recorder, req)
	return recorder
}

// tenant operator 不提供 tenant_id 时只能落到认证 scope。改成请求体/零值来源后，传播断言转红。
func TestTenantOperatorDefaultsToAuthenticatedScope(t *testing.T) {
	service := &stubService{}
	recorder := request(t, tenantIdentity(7, 42), service, http.MethodGet, "/", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !service.listCalled || service.lastTarget.Scope != registry.ModelScopeTenant || service.lastTarget.TenantID != 42 {
		t.Fatalf("目标域传播错误：called=%v target=%+v", service.listCalled, service.lastTarget)
	}
	if service.lastAccess.Role != admin.RoleTenantOperator || service.lastAccess.ScopeTenantID != 42 || service.lastAccess.Actor != "admin_token:7" {
		t.Fatalf("认证上下文传播错误：%+v", service.lastAccess)
	}
}

// platform admin 管 tenant 时必须显式给 tenant_id，避免零值或隐式默认租户。
func TestPlatformAdminTenantScopeRequiresExplicitTenant(t *testing.T) {
	service := &stubService{}
	recorder := request(t, platformIdentity(7), service, http.MethodGet, "/", "")
	if recorder.Code != http.StatusBadRequest || service.listCalled {
		t.Fatalf("code=%d listCalled=%v want 400/false", recorder.Code, service.listCalled)
	}
}

// HTTP 第一道 IDOR 防线必须在 service 前拒绝跨租户读、改、删。
// 去掉 CanIssueForTenant 后三个请求都会进入 service，对应 called 断言转红。
func TestTenantOperatorCrossTenantReadUpdateDeleteForbidden(t *testing.T) {
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/9?tenant_id=99", ""},
		{http.MethodPatch, "/9?tenant_id=99", `{"status":"disabled"}`},
		{http.MethodDelete, "/9?tenant_id=99", ""},
	}
	for _, test := range cases {
		service := &stubService{}
		recorder := request(t, tenantIdentity(7, 42), service, test.method, test.path, test.body)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s code=%d want 403; body=%s", test.method, recorder.Code, recorder.Body.String())
		}
		if service.getCalled || service.updateCalled || service.deleteCalled {
			t.Errorf("%s 跨租户仍调用 service：%+v", test.method, service)
		}
	}
}

// tenant operator 的 global 读由 HTTP 门确认身份 scope 后交给 service 检查继承策略，
// Create/PATCH/DELETE 则必须在 HTTP 门直接拒绝。放开写守卫后任一 service called 会转红。
func TestTenantOperatorGlobalIsReadOnly(t *testing.T) {
	readService := &stubService{model: registry.AdminModel{ID: 9, Scope: registry.ModelScopeGlobal}}
	read := request(t, tenantIdentity(7, 42), readService, http.MethodGet, "/9?scope=global", "")
	if read.Code != http.StatusOK || !readService.getCalled {
		t.Fatalf("global 只读 code=%d called=%v body=%s", read.Code, readService.getCalled, read.Body.String())
	}
	if readService.lastTarget.Scope != registry.ModelScopeGlobal || readService.lastTarget.TenantID != 0 {
		t.Fatalf("global 目标错误：%+v", readService.lastTarget)
	}

	writes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/?scope=global", `{"canonical_id":"g","protocol_family":"openai_chat","default_provider_model_id":"p"}`},
		{http.MethodPatch, "/9?scope=global", `{"status":"disabled"}`},
		{http.MethodDelete, "/9?scope=global", ""},
	}
	for _, test := range writes {
		service := &stubService{}
		recorder := request(t, tenantIdentity(7, 42), service, test.method, test.path, test.body)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s global 写 code=%d want 403", test.method, recorder.Code)
		}
		if service.createCalled || service.updateCalled || service.deleteCalled {
			t.Errorf("%s global 写仍调用 service", test.method)
		}
	}
}

func TestTenantOperatorGlobalReadRequiresAuthenticatedTenantScope(t *testing.T) {
	service := &stubService{}
	recorder := request(t, tenantIdentity(7, 0), service, http.MethodGet, "/9?scope=global", "")
	if recorder.Code != http.StatusForbidden || service.getCalled {
		t.Fatalf("缺租户 scope 的 global 读 code=%d getCalled=%v want 403/false", recorder.Code, service.getCalled)
	}
}

// platform admin 创建 global 时应用默认字段，并且 actor 只能来自认证身份。
// 改动任一默认值或从请求体接受 actor 后，入参断言转红。
func TestPlatformAdminCreatesGlobalWithDefaults(t *testing.T) {
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	service := &stubService{model: registry.AdminModel{
		ID: 11, Scope: registry.ModelScopeGlobal, CanonicalID: "global/manual",
		ProtocolFamily: "openai_chat", DefaultProviderModelID: "provider-id",
		DefaultRequestTimeoutMS: 60000, PricingClass: "standard", ModelOwner: "HUAKAI",
		Status: "active", Capabilities: map[string]bool{}, CreatedAt: now, UpdatedAt: now,
	}}
	recorder := request(t, platformIdentity(8), service, http.MethodPost, "/?scope=global",
		`{"canonical_id":" global/manual ","protocol_family":" openai_chat ","default_provider_model_id":" provider-id "}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("code=%d want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	input := service.lastCreate
	if !service.createCalled || input.Target.Scope != registry.ModelScopeGlobal || input.Target.TenantID != 0 {
		t.Fatalf("global 创建目标错误：called=%v input=%+v", service.createCalled, input)
	}
	if input.Access.Role != admin.RolePlatformAdmin || input.Access.Actor != "admin_token:8" {
		t.Fatalf("认证上下文错误：%+v", input.Access)
	}
	if input.CanonicalID != "global/manual" || input.ProtocolFamily != "openai_chat" || input.DefaultProviderModelID != "provider-id" ||
		input.DefaultContextWindow != 0 || input.DefaultRequestTimeoutMS != 60000 || input.PricingClass != "standard" ||
		input.ModelOwner != "HUAKAI" || input.Status != "active" {
		t.Fatalf("默认/规范化字段错误：%+v", input)
	}
}

// platform admin 的 tenant 创建必须把显式 tenant_id 经过 CanIssue 后传到 service。
func TestPlatformAdminCreatesExplicitTenantModel(t *testing.T) {
	service := &stubService{model: registry.AdminModel{ID: 12}}
	recorder := request(t, platformIdentity(8), service, http.MethodPost, "/?tenant_id=77",
		`{"canonical_id":"tenant/manual","protocol_family":"openai_chat","default_provider_model_id":"provider"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("code=%d want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if service.lastCreate.Target.Scope != registry.ModelScopeTenant || service.lastCreate.Target.TenantID != 77 {
		t.Fatalf("tenant 目标错误：%+v", service.lastCreate.Target)
	}
}

// 严格 JSON 同时拒绝未知字段与尾随第二个对象；放宽任一 decoder 约束后对应请求会调用 service。
func TestCreateRejectsUnknownAndTrailingJSON(t *testing.T) {
	bodies := []string{
		`{"canonical_id":"x","protocol_family":"openai_chat","default_provider_model_id":"p","actor":"伪造"}`,
		`{"canonical_id":"x","protocol_family":"openai_chat","default_provider_model_id":"p"}{"status":"disabled"}`,
	}
	for _, body := range bodies {
		service := &stubService{}
		recorder := request(t, platformIdentity(8), service, http.MethodPost, "/?scope=global", body)
		if recorder.Code != http.StatusBadRequest || service.createCalled {
			t.Errorf("body=%q code=%d createCalled=%v want 400/false", body, recorder.Code, service.createCalled)
		}
	}
}

// PATCH 只传播显式字段；把 nil 字段错误地填成零值后这些指针断言转红。
func TestPatchPropagatesOnlyProvidedFields(t *testing.T) {
	service := &stubService{model: registry.AdminModel{ID: 13}}
	recorder := request(t, tenantIdentity(7, 42), service, http.MethodPatch, "/13",
		`{"default_context_window":0,"model_owner":" 新归属 ","status":"disabled"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	input := service.lastUpdate
	if !service.updateCalled || input.ID != 13 || input.DefaultContextWindow == nil || *input.DefaultContextWindow != 0 ||
		input.ModelOwner == nil || *input.ModelOwner != " 新归属 " || input.Status == nil || *input.Status != "disabled" {
		t.Fatalf("PATCH 显式字段传播错误：%+v", input)
	}
	if input.DefaultProviderModelID != nil || input.DefaultRequestTimeoutMS != nil || input.PricingClass != nil || input.ProtocolFamily != nil {
		t.Fatalf("PATCH 未提供字段被填充：%+v", input)
	}
}

func TestPatchRejectsEmptyBody(t *testing.T) {
	service := &stubService{}
	recorder := request(t, tenantIdentity(7, 42), service, http.MethodPatch, "/13", `{}`)
	if recorder.Code != http.StatusBadRequest || service.updateCalled {
		t.Fatalf("code=%d updateCalled=%v want 400/false", recorder.Code, service.updateCalled)
	}
}

// 哨兵错误必须一一映射成可判别 HTTP 状态；任一 case 合并到 503 都会转红。
func TestServiceErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{registry.ErrModelAdminInvalid, http.StatusBadRequest},
		{registry.ErrModelAdminForbidden, http.StatusForbidden},
		{registry.ErrModelAdminNotFound, http.StatusNotFound},
		{registry.ErrConflict, http.StatusConflict},
		{errors.New("存储故障"), http.StatusServiceUnavailable},
	}
	for _, test := range cases {
		service := &stubService{err: test.err}
		recorder := request(t, platformIdentity(8), service, http.MethodGet, "/9?scope=global", "")
		if recorder.Code != test.want {
			t.Errorf("err=%v code=%d want %d", test.err, recorder.Code, test.want)
		}
	}
}

func TestListReturnsNumericIDAndFullFields(t *testing.T) {
	tenantID := int64(42)
	mode := "reasoning"
	maxOutput := int32(4096)
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	service := &stubService{models: []registry.AdminModel{{
		ID: 91, TenantID: &tenantID, Scope: registry.ModelScopeTenant,
		CanonicalID: "tenant/model", ProtocolFamily: "openai_chat", DefaultProviderModelID: "provider/model",
		DefaultContextWindow: 128000, DefaultRequestTimeoutMS: 90000, PricingClass: "premium",
		ModelOwner: "owner", Capabilities: map[string]bool{"tools": true}, MaxOutputTokens: &maxOutput,
		ModelMode: &mode, Status: "disabled", CreatedAt: now, UpdatedAt: now,
	}}}
	recorder := request(t, tenantIdentity(7, 42), service, http.MethodGet, "/", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Object string          `json:"object"`
		Items  []modelResponse `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析响应：%v", err)
	}
	if envelope.Object != "admin_models_list" || len(envelope.Items) != 1 {
		t.Fatalf("列表信封错误：%+v", envelope)
	}
	item := envelope.Items[0]
	if item.ID != 91 || item.TenantID == nil || *item.TenantID != 42 || item.CanonicalID != "tenant/model" ||
		item.DefaultRequestTimeoutMS != 90000 || item.MaxOutputTokens == nil || *item.MaxOutputTokens != 4096 ||
		item.ModelMode == nil || *item.ModelMode != "reasoning" || item.Status != "disabled" {
		t.Fatalf("全字段列表映射错误：%+v", item)
	}
}

func TestInvalidPathIDAndUnconfiguredDependenciesFailClosed(t *testing.T) {
	service := &stubService{}
	recorder := request(t, tenantIdentity(7, 42), service, http.MethodGet, "/zero", "")
	if recorder.Code != http.StatusBadRequest || service.getCalled {
		t.Fatalf("非法 id code=%d getCalled=%v", recorder.Code, service.getCalled)
	}

	req := httptest.NewRequest(http.MethodGet, "/?tenant_id=42", nil)
	unconfigured := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(unconfigured, req)
	if unconfigured.Code != http.StatusServiceUnavailable {
		t.Fatalf("未接线 code=%d want 503", unconfigured.Code)
	}
}
