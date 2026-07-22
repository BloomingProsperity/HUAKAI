package accountintakehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type accountIntakeAuthStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s accountIntakeAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type accountIntakeServiceStub struct {
	planInput     accountintake.PlanInput
	executeInput  accountintake.ExecuteInput
	planResult    accountintake.PlanResult
	executeResult accountintake.ExecutionResult
	planErr       error
	executeErr    error
	planCalls     int
	executeCalls  int
}

func (s *accountIntakeServiceStub) Plan(_ context.Context, in accountintake.PlanInput) (accountintake.PlanResult, error) {
	s.planCalls++
	s.planInput = in
	return s.planResult, s.planErr
}

func (s *accountIntakeServiceStub) Execute(_ context.Context, in accountintake.ExecuteInput) (accountintake.ExecutionResult, error) {
	s.executeCalls++
	s.executeInput = in
	return s.executeResult, s.executeErr
}

func TestAdminAccountIntakePlanStrictDecodeAndRedactedResponse(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{
		PlanHash: strings.Repeat("b", 64),
		Plan:     intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceJSON},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{\"api_key\":\"secret\"}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.planInput.TenantID != 7 || service.planInput.Account.ProviderID != 2 {
		t.Fatalf("service input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("响应不应回显导入内容：%s", rec.Body.String())
	}

	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan",
		`{"tenant_id":7,"source_kind":"json_import","content":"x","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"},"unknown":true}`)
	if rec.Code != http.StatusBadRequest || service.planCalls != 1 {
		t.Fatalf("未知字段 status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}

	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan", body+` {}`)
	if rec.Code != http.StatusBadRequest || service.planCalls != 1 {
		t.Fatalf("尾随 JSON status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}
}

func TestAdminAccountIntakePlanPassesClaudeSetupTokenSourceWithoutOverrides(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{
		PlanHash: strings.Repeat("c", 64),
		Plan:     intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceClaudeSetupToken},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"source_kind":"claude_setup_token","default_vendor":"attacker","default_auth_mode":"api_key","content":"setup-secret","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.planInput.SourceKind != intake.SourceClaudeSetupToken || service.planInput.Content != "setup-secret" {
		t.Fatalf("service input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), "setup-secret") {
		t.Fatalf("响应不应回显 Setup Token：%s", rec.Body.String())
	}
}

func TestAdminAccountIntakeExecuteDoesNotEchoClaudeSetupToken(t *testing.T) {
	service := &accountIntakeServiceStub{executeResult: accountintake.ExecutionResult{
		Summary: accountintake.ExecutionSummary{Created: 1},
		Items:   []accountintake.ExecutionItem{{Status: accountintake.StatusCreated, ProviderAccountID: 101, AccountCredentialID: 202}},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	secret := "setup-secret-execute"
	body := `{"tenant_id":7,"source_kind":"claude_setup_token","content":"` + secret + `","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"},"plan_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","confirmations":["confirm_weak_identity"]}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.PlanInput.Content != secret {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("执行响应不应回显 Setup Token：%s", rec.Body.String())
	}
}

func TestAdminAccountIntakeExecuteMapsPlanChangeAndAuditIdentity(t *testing.T) {
	service := &accountIntakeServiceStub{executeErr: accountintake.ErrPlanChanged}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"},"plan_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","confirmations":["confirm_weak_identity"],"reason":"批量接入"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/execute", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.ActorID != "admin_token:9" ||
		service.executeInput.ActorRole != admin.RoleTenantOperator ||
		service.executeInput.PlanHash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
}

func TestAdminAccountIntakeAcceptsAuthorizedSessionAndRejectsOversizedBody(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{PlanHash: strings.Repeat("a", 64)}}
	sessionHandler := accountIntakeTestHandler(accountIntakeAuthStub{identity: admin.AdminIdentity{
		Source: admin.AdminSourceSession, UserID: 5, Role: admin.RoleTenantOperator, ScopeTenantID: 7,
	}}, service)
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"}}`
	rec := doAccountIntakeRequest(sessionHandler, "/admin/v1/credentials/account-imports/plan", body)
	if rec.Code != http.StatusOK || service.planCalls != 1 {
		t.Fatalf("session status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}

	tokenHandler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	oversized := `{"content":"` + strings.Repeat("x", accountIntakeBodyLimit) + `"}`
	rec = doAccountIntakeRequest(tokenHandler, "/admin/v1/credentials/account-imports/plan", oversized)
	if rec.Code != http.StatusRequestEntityTooLarge || service.planCalls != 1 {
		t.Fatalf("oversized status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}
}

func TestAdminAccountIntakePlatformOnlyOwnTenantAndTenantOperatorOnlyScopedTenant(t *testing.T) {
	service := &accountIntakeServiceStub{}
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"}}`
	tests := []struct {
		name     string
		identity admin.AdminIdentity
		path     string
		body     string
	}{
		{
			name: "租户令牌必须有正数作用域",
			identity: admin.AdminIdentity{
				Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RoleTenantOperator,
			},
			path: "/admin/v1/credentials/account-imports/plan",
			body: body,
		},
		{
			name:     "租户令牌不得请求其他租户",
			identity: tenantTokenIdentity(8),
			path:     "/admin/v1/credentials/account-imports/plan",
			body:     body,
		},
		{
			name:     "执行入口同样拒绝跨租户",
			identity: tenantTokenIdentity(8),
			path:     "/admin/v1/credentials/account-imports/execute",
			body: strings.TrimSuffix(body, "}") +
				`,"plan_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tc.identity}, service)
			rec := doAccountIntakeRequest(handler, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	platformHandler := accountIntakeTestHandler(accountIntakeAuthStub{identity: admin.AdminIdentity{
		Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RolePlatformAdmin,
	}}, service)
	rec := doAccountIntakeRequest(platformHandler, "/admin/v1/credentials/account-imports/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("部署者导入平台自有租户 status=%d body=%s", rec.Code, rec.Body.String())
	}
	crossBody := strings.Replace(body, `"tenant_id":7`, `"tenant_id":8`, 1)
	rec = doAccountIntakeRequest(platformHandler, "/admin/v1/credentials/account-imports/plan", crossBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("部署者不得代下级租户导入,status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.executeCalls != 0 {
		t.Fatalf("仅平台自有租户请求应触发 service：plan=%d execute=%d", service.planCalls, service.executeCalls)
	}
}

func TestAdminAccountIntakeAuthBackendFailure(t *testing.T) {
	service := &accountIntakeServiceStub{}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{err: admin.ErrAdminBackend}, service)
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan", `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminAccountIntakeRequiresExplicitTenantCapability(t *testing.T) {
	service := &accountIntakeServiceStub{}
	capabilities := accountIntakeCapabilityStub{allowed: false}
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth:    accountIntakeAuthStub{identity: tenantTokenIdentity(7)},
			Service: service, Capabilities: capabilities,
		})
	})
	rec := doAccountIntakeRequest(router, "/admin/v1/credentials/account-imports/plan", `{}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "tenant_capability_not_granted") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 0 {
		t.Fatalf("未授权租户触发 service：%d", service.planCalls)
	}
}

func TestAdminAccountIntakeDoesNotExposeBackendError(t *testing.T) {
	service := &accountIntakeServiceStub{planErr: errors.New("pq: relation internal_secret_table does not exist")}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/plan", body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "internal_secret_table") ||
		!strings.Contains(rec.Body.String(), "temporarily unavailable") {
		t.Fatalf("响应泄露底层错误或缺少稳定消息：%s", rec.Body.String())
	}
}

func TestAdminAccountIntakeMapsCodexLaneConfigurationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "没有可运行车道", err: accountintake.ErrCodexLaneAbsent, code: "codex_lane_not_configured"},
		{name: "存在多条车道", err: accountintake.ErrCodexLaneMany, code: "codex_lane_ambiguous"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &accountIntakeServiceStub{planErr: tc.err}
			handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
			rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex/plan",
				`{"tenant_id":7,"content":"access-token"}`)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func accountIntakeTestHandler(auth AdminAuth, service AdminAccountIntakeService) http.Handler {
	r := chi.NewRouter()
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{Auth: auth, Service: service, Capabilities: allowAccountIntakeCapability{}, PlatformTenantID: 7})
	})
	return r
}

type allowAccountIntakeCapability struct{}

func (allowAccountIntakeCapability) Allowed(context.Context, int64, string) (bool, error) {
	return true, nil
}

type accountIntakeCapabilityStub struct {
	allowed bool
	err     error
}

func (s accountIntakeCapabilityStub) Allowed(context.Context, int64, string) (bool, error) {
	return s.allowed, s.err
}

func doAccountIntakeRequest(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func tenantTokenIdentity(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{
		Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID,
	}
}
