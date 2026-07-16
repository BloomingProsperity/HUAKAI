package modelroutingadminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

type routeTestAuth struct {
	identity admin.AdminIdentity
	err      error
}

func NewRouter(deps Deps) http.Handler {
	router := chi.NewRouter()
	MountRoutes(router, deps)
	return router
}

func (a routeTestAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.identity, a.err
}

type routeTestService struct {
	listedTenant int64
	created      CreateInput
	updated      UpdateInput
	deletedID    int64
	deletedTen   int64
	createCalled bool
	updateCalled bool
	result       Override
	err          error
}

func (s *routeTestService) List(_ context.Context, tenantID int64) ([]Override, error) {
	s.listedTenant = tenantID
	if s.err != nil {
		return nil, s.err
	}
	return []Override{s.result}, nil
}

func (s *routeTestService) Create(_ context.Context, input CreateInput) (Override, error) {
	s.createCalled = true
	s.created = input
	return s.result, s.err
}

func (s *routeTestService) Update(_ context.Context, input UpdateInput) (Override, error) {
	s.updateCalled = true
	s.updated = input
	return s.result, s.err
}

func (s *routeTestService) Delete(_ context.Context, id, tenantID int64) error {
	s.deletedID = id
	s.deletedTen = tenantID
	return s.err
}

func routeTestPlatformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 7, Role: admin.RolePlatformAdmin}
}

func routeTestTenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 8, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

func performRouteTest(t *testing.T, identity admin.AdminIdentity, service *routeTestService, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	NewRouter(Deps{Auth: routeTestAuth{identity: identity}, Service: service}).ServeHTTP(rec, req)
	return rec
}

// 创建映射必须把租户、池、模型、账号数组与 enabled 原样送进服务层。
// 变异：漏掉 provider_account_ids 或 enabled 的 DTO/赋值，断言立即转红。
func TestCreateMapsAllPinFields(t *testing.T) {
	service := &routeTestService{result: Override{ID: 31}}
	rec := performRouteTest(t, routeTestPlatformAdmin(), service, http.MethodPost, "/?tenant_id=42",
		`{"pool_group_id":9,"model":"gpt-pin","provider_account_ids":[11,13],"enabled":false}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码=%d，期望 201；响应=%s", rec.Code, rec.Body.String())
	}
	got := service.created
	if !service.createCalled || got.TenantID != 42 || got.PoolGroupID != 9 || got.Model != "gpt-pin" || got.Enabled {
		t.Fatalf("创建字段映射错误：called=%v input=%+v", service.createCalled, got)
	}
	if len(got.ProviderAccountIDs) != 2 || got.ProviderAccountIDs[0] != 11 || got.ProviderAccountIDs[1] != 13 {
		t.Fatalf("账号数组映射错误：%v", got.ProviderAccountIDs)
	}
}

// PATCH 只传播显式字段，避免编辑 enabled 时把账号数组清空。
// 变异：把缺省 provider_account_ids 变成空切片，nil 断言转红。
func TestUpdatePreservesOmittedFields(t *testing.T) {
	service := &routeTestService{result: Override{ID: 31}}
	rec := performRouteTest(t, routeTestTenantOperator(42), service, http.MethodPatch, "/31", `{"enabled":false}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d，期望 200；响应=%s", rec.Code, rec.Body.String())
	}
	if !service.updateCalled || service.updated.ID != 31 || service.updated.TenantID != 42 {
		t.Fatalf("更新主键或租户传播错误：%+v", service.updated)
	}
	if service.updated.ProviderAccountIDs != nil {
		t.Fatalf("省略账号数组时必须保持 nil，实际=%v", *service.updated.ProviderAccountIDs)
	}
	if service.updated.Enabled == nil || *service.updated.Enabled {
		t.Fatalf("enabled=false 未精确传播：%v", service.updated.Enabled)
	}
}

// 账号数组必须非空且全部为正数，避免落一个看似启用但实际不收窄候选的空 pin。
func TestCreateRejectsInvalidAccountIDs(t *testing.T) {
	for _, body := range []string{
		`{"pool_group_id":9,"model":"gpt-pin","provider_account_ids":[]}`,
		`{"pool_group_id":9,"model":"gpt-pin","provider_account_ids":[11,0]}`,
	} {
		service := &routeTestService{}
		rec := performRouteTest(t, routeTestPlatformAdmin(), service, http.MethodPost, "/?tenant_id=42", body)
		if rec.Code != http.StatusBadRequest || service.createCalled {
			t.Fatalf("非法账号数组 body=%s，状态码=%d called=%v，期望 400/false", body, rec.Code, service.createCalled)
		}
	}
}

// tenant_operator 携带其它 tenant_id 必须在服务调用前被共享 admin gate 拒绝。
func TestTenantOperatorCannotCrossTenant(t *testing.T) {
	service := &routeTestService{}
	rec := performRouteTest(t, routeTestTenantOperator(42), service, http.MethodGet, "/?tenant_id=99", "")
	if rec.Code != http.StatusForbidden || service.listedTenant != 0 {
		t.Fatalf("跨租户状态码=%d listedTenant=%d，期望 403/0", rec.Code, service.listedTenant)
	}
}

// 列表必须使用 items 信封并包含账号数组，防止前端把真实数据误判为空态。
func TestListReturnsEnvelope(t *testing.T) {
	service := &routeTestService{result: Override{ID: 31, TenantID: 42, PoolGroupID: 9, Model: "gpt-pin", ProviderAccountIDs: []int64{11}, Enabled: true}}
	rec := performRouteTest(t, routeTestTenantOperator(42), service, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK || service.listedTenant != 42 {
		t.Fatalf("状态码=%d listedTenant=%d，期望 200/42", rec.Code, service.listedTenant)
	}
	var envelope struct {
		Items []overrideResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析列表响应：%v", err)
	}
	if len(envelope.Items) != 1 || envelope.Items[0].ID != 31 || len(envelope.Items[0].ProviderAccountIDs) != 1 || envelope.Items[0].ProviderAccountIDs[0] != 11 {
		t.Fatalf("列表响应错误：%+v", envelope.Items)
	}
}

// 删除必须同时下推资源 ID 与 gate 解析出的租户 ID。
func TestDeletePropagatesTenantAndID(t *testing.T) {
	service := &routeTestService{}
	rec := performRouteTest(t, routeTestTenantOperator(42), service, http.MethodDelete, "/31", "")
	if rec.Code != http.StatusNoContent || service.deletedID != 31 || service.deletedTen != 42 {
		t.Fatalf("状态码=%d delete=(%d,%d)，期望 204/(31,42)", rec.Code, service.deletedID, service.deletedTen)
	}
}

// 服务哨兵必须映射成可操作的 HTTP 错误，不得把租户归属失败泄成 500。
func TestServiceErrorsAreDiscriminated(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrPoolNotOwned, http.StatusUnprocessableEntity},
		{ErrAccountsNotOwned, http.StatusUnprocessableEntity},
	}
	for _, testCase := range cases {
		service := &routeTestService{err: testCase.err}
		rec := performRouteTest(t, routeTestPlatformAdmin(), service, http.MethodPost, "/?tenant_id=42",
			`{"pool_group_id":9,"model":"gpt-pin","provider_account_ids":[11]}`)
		if rec.Code != testCase.want {
			t.Errorf("错误=%v，状态码=%d，期望 %d", testCase.err, rec.Code, testCase.want)
		}
	}
}

// 未注入 Auth 或 Service 时必须 fail closed，避免挂出绕 gate 的半活路由。
func TestMissingDependenciesFailClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?tenant_id=42", nil)
	rec := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d，期望 503", rec.Code)
	}
}

func TestUnauthorizedIs401(t *testing.T) {
	service := &routeTestService{}
	req := httptest.NewRequest(http.MethodGet, "/?tenant_id=42", nil)
	rec := httptest.NewRecorder()
	NewRouter(Deps{Auth: routeTestAuth{err: admin.ErrAdminUnauthorized}, Service: service}).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d，期望 401", rec.Code)
	}
}

func TestUnexpectedServiceErrorIs503(t *testing.T) {
	service := &routeTestService{err: errors.New("db down")}
	rec := performRouteTest(t, routeTestPlatformAdmin(), service, http.MethodGet, "/?tenant_id=42", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d，期望 503", rec.Code)
	}
}
