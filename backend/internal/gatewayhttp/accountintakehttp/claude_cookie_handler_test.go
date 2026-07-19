package accountintakehttp

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type claudeCookieServiceStub struct {
	planInput     accountintake.CookiePlanInput
	executeInput  accountintake.CookieExecuteInput
	planResult    accountintake.CookiePlanResult
	executeResult accountintake.ExecutionResult
	planErr       error
	executeErr    error
	planCalls     int
	executeCalls  int
}

func (s *claudeCookieServiceStub) Plan(_ context.Context, in accountintake.CookiePlanInput) (accountintake.CookiePlanResult, error) {
	s.planCalls++
	s.planInput = in
	return s.planResult, s.planErr
}

func (s *claudeCookieServiceStub) Execute(_ context.Context, in accountintake.CookieExecuteInput) (accountintake.ExecutionResult, error) {
	s.executeCalls++
	s.executeInput = in
	return s.executeResult, s.executeErr
}

func TestClaudeCookiePlan传递租户身份且不回显Cookie(t *testing.T) {
	secret := "sk-ant-cookie-sentinel"
	service := &claudeCookieServiceStub{planResult: accountintake.CookiePlanResult{
		FlowID: "4f398b58-8a70-42aa-8592-bf9d2d40acc0", ExpiresAt: time.Date(2026, 7, 19, 12, 15, 0, 0, time.UTC),
		OrganizationID: "org-selected", AuthMode: "claude_ai_oauth", PlanHash: strings.Repeat("a", 64),
		Plan: intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceJSON},
	}}
	handler := claudeCookieTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"session_key":"` + secret + `","organization_id":"org-selected","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"},"reason":"Cookie自动导入"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.planInput.TenantID != 7 || service.planInput.ActorID != "admin_token:9" ||
		service.planInput.ActorRole != "tenant_operator" || service.planInput.SessionKey != secret || service.planInput.OrganizationID != "org-selected" {
		t.Fatalf("plan input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("响应泄漏 Cookie：%s", rec.Body.String())
	}

	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/plan", strings.TrimSuffix(body, "}")+`,"vendor":"openai"}`)
	if rec.Code != http.StatusBadRequest || service.planCalls != 1 {
		t.Fatalf("未知覆盖字段 status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}
}

func TestClaudeCookiePlan多组织要求明确选择且不泄漏Cookie(t *testing.T) {
	secret := "cookie-must-not-leak"
	service := &claudeCookieServiceStub{planErr: &claudecookie.OrganizationChoiceError{Organizations: []claudecookie.Organization{
		{ID: "org-a", Name: "甲"}, {ID: "org-b", Name: "乙", Kind: "team"},
	}}}
	handler := claudeCookieTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"session_key":"` + secret + `","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/plan", body)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "claude_organization_choice_required") ||
		!strings.Contains(rec.Body.String(), "org-a") || !strings.Contains(rec.Body.String(), "org-b") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("冲突响应泄漏 Cookie：%s", rec.Body.String())
	}
}

func TestClaudeCookieExecute只接受一次性流程引用(t *testing.T) {
	service := &claudeCookieServiceStub{executeResult: accountintake.ExecutionResult{
		PlanHash: strings.Repeat("b", 64), Summary: accountintake.ExecutionSummary{Created: 1},
		Items: []accountintake.ExecutionItem{{Status: accountintake.StatusCreated, ProviderAccountID: 101}},
	}}
	handler := claudeCookieTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"flow_id":"4f398b58-8a70-42aa-8592-bf9d2d40acc0","plan_hash":"` + strings.Repeat("b", 64) + `","confirmations":["confirm_weak_identity"],"reason":"确认导入"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.TenantID != 7 || service.executeInput.ActorID != "admin_token:9" ||
		service.executeInput.FlowID != "4f398b58-8a70-42aa-8592-bf9d2d40acc0" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}

	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/execute", strings.TrimSuffix(body, "}")+`,"session_key":"forbidden"}`)
	if rec.Code != http.StatusBadRequest || service.executeCalls != 1 {
		t.Fatalf("执行入口接受了 Cookie status=%d calls=%d body=%s", rec.Code, service.executeCalls, rec.Body.String())
	}
}

func TestClaudeCookie拒绝跨租户且映射短期流程错误(t *testing.T) {
	service := &claudeCookieServiceStub{}
	handler := claudeCookieTestHandler(service, tenantTokenIdentity(8))
	body := `{"tenant_id":7,"session_key":"secret","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/plan", body)
	if rec.Code != http.StatusForbidden || service.planCalls != 0 {
		t.Fatalf("跨租户 status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}

	service.executeErr = accountintake.ErrStagedCredentialReplay
	handler = claudeCookieTestHandler(service, tenantTokenIdentity(7))
	executeBody := `{"tenant_id":7,"flow_id":"4f398b58-8a70-42aa-8592-bf9d2d40acc0","plan_hash":"` + strings.Repeat("c", 64) + `"}`
	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-cookie/execute", executeBody)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "credential_flow_replayed") {
		t.Fatalf("重放 status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func claudeCookieTestHandler(cookieService *claudeCookieServiceStub, identity admin.AdminIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth: accountIntakeAuthStub{identity: identity}, Service: &accountIntakeServiceStub{},
			CookieService: cookieService, Capabilities: allowAccountIntakeCapability{},
		})
	})
	return r
}
