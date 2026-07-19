package accountintakehttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/crssource"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type crsServiceStub struct {
	planInput    accountintake.CRSPlanInput
	executeInput accountintake.CRSExecuteInput
	planResult   accountintake.CRSPlanResult
	execResult   accountintake.CRSExecutionResult
	planErr      error
	execErr      error
	planCalls    int
	execCalls    int
}

func (s *crsServiceStub) Plan(_ context.Context, in accountintake.CRSPlanInput) (accountintake.CRSPlanResult, error) {
	s.planCalls++
	s.planInput = in
	return s.planResult, s.planErr
}

func (s *crsServiceStub) Execute(_ context.Context, in accountintake.CRSExecuteInput) (accountintake.CRSExecutionResult, error) {
	s.execCalls++
	s.executeInput = in
	return s.execResult, s.execErr
}

func TestCRSPlan默认同步代理且不回显密码(t *testing.T) {
	secret := "crs-password-sentinel"
	service := &crsServiceStub{planResult: accountintake.CRSPlanResult{
		SourceRef: "abc123", Summary: accountintake.CRSPlanSummary{Ready: 1},
	}}
	handler := crsTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"base_url":"https://crs.example","username":"owner","password":"` + secret + `","destinations":{"claude":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}},"reason":"迁移账号"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/crs/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || !service.planInput.SyncProxies || service.planInput.Password != secret ||
		service.planInput.ActorID != "admin_token:9" || service.planInput.TenantID != 7 {
		t.Fatalf("plan input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("响应泄漏 CRS 密码：%s", rec.Body.String())
	}
}

func TestCRSExecute只接受一次性流程并拒绝未知字段(t *testing.T) {
	service := &crsServiceStub{execResult: accountintake.CRSExecutionResult{
		Summary: accountintake.CRSExecutionSummary{Completed: 1},
	}}
	handler := crsTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"entries":[{"flow_id":"4f398b58-8a70-42aa-8592-bf9d2d40acc0","plan_hash":"` + strings.Repeat("a", 64) + `","confirmations":["confirm_weak_identity"]}],"reason":"确认迁移"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/crs/execute", body)
	if rec.Code != http.StatusOK || service.execCalls != 1 || len(service.executeInput.Entries) != 1 {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", rec.Code, service.execCalls, service.executeInput, rec.Body.String())
	}

	rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/crs/execute", strings.TrimSuffix(body, "}")+`,"password":"forbidden"}`)
	if rec.Code != http.StatusBadRequest || service.execCalls != 1 {
		t.Fatalf("执行入口接受来源密码 status=%d calls=%d body=%s", rec.Code, service.execCalls, rec.Body.String())
	}
}

func TestCRS错误映射不泄漏底层信息(t *testing.T) {
	service := &crsServiceStub{planErr: crssource.ErrEndpointDenied}
	handler := crsTestHandler(service, tenantTokenIdentity(7))
	body := `{"tenant_id":7,"base_url":"https://denied.example","username":"owner","password":"secret","destinations":{"claude":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/crs/plan", body)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "crs_endpoint_denied") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func crsTestHandler(service *crsServiceStub, identity admin.AdminIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth: accountIntakeAuthStub{identity: identity}, Service: &accountIntakeServiceStub{},
			CRSService: service, Capabilities: allowAccountIntakeCapability{},
		})
	})
	return r
}
