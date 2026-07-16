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
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: platformTokenIdentity()}, service)
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

func TestAdminAccountIntakeExecuteMapsPlanChangeAndAuditIdentity(t *testing.T) {
	service := &accountIntakeServiceStub{executeErr: accountintake.ErrPlanChanged}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: platformTokenIdentity()}, service)
	body := `{"tenant_id":7,"source_kind":"json_import","content":"{}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"api_key"},"plan_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","confirmations":["confirm_weak_identity"],"reason":"批量接入"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/execute", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.ActorID != "admin_token:9" ||
		service.executeInput.ActorRole != admin.RolePlatformAdmin ||
		service.executeInput.PlanHash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
}

func TestAdminAccountIntakeRejectsSessionAndOversizedBody(t *testing.T) {
	service := &accountIntakeServiceStub{}
	sessionHandler := accountIntakeTestHandler(accountIntakeAuthStub{identity: admin.AdminIdentity{
		Source: admin.AdminSourceSession, UserID: 5, Role: admin.RolePlatformAdmin,
	}}, service)
	rec := doAccountIntakeRequest(sessionHandler, "/admin/v1/credentials/account-imports/plan", `{}`)
	if rec.Code != http.StatusForbidden || service.planCalls != 0 {
		t.Fatalf("session status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
	}

	tokenHandler := accountIntakeTestHandler(accountIntakeAuthStub{identity: platformTokenIdentity()}, service)
	oversized := `{"content":"` + strings.Repeat("x", accountIntakeBodyLimit) + `"}`
	rec = doAccountIntakeRequest(tokenHandler, "/admin/v1/credentials/account-imports/plan", oversized)
	if rec.Code != http.StatusRequestEntityTooLarge || service.planCalls != 0 {
		t.Fatalf("oversized status=%d calls=%d body=%s", rec.Code, service.planCalls, rec.Body.String())
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

func TestAdminAccountIntakeDoesNotExposeBackendError(t *testing.T) {
	service := &accountIntakeServiceStub{planErr: errors.New("pq: relation internal_secret_table does not exist")}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: platformTokenIdentity()}, service)
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

func accountIntakeTestHandler(auth AdminAuth, service AdminAccountIntakeService) http.Handler {
	r := chi.NewRouter()
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{Auth: auth, Service: service})
	})
	return r
}

func doAccountIntakeRequest(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func platformTokenIdentity() admin.AdminIdentity {
	return admin.AdminIdentity{
		Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RolePlatformAdmin,
	}
}
