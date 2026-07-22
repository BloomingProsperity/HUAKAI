package accountintakehttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountbundle"
)

type accountBundleServiceStub struct {
	exportPlanInput    accountbundle.ExportPlanInput
	exportExecuteInput accountbundle.ExportExecuteInput
	importPlanInput    accountbundle.ImportPlanInput
	importExecuteInput accountbundle.ImportExecuteInput
	err                error
}

func (s *accountBundleServiceStub) PlanExport(_ context.Context, in accountbundle.ExportPlanInput) (accountbundle.ExportPlan, error) {
	s.exportPlanInput = in
	return accountbundle.ExportPlan{PlanHash: strings.Repeat("a", 64), Ready: 1}, s.err
}

func (s *accountBundleServiceStub) ExecuteExport(_ context.Context, in accountbundle.ExportExecuteInput) (accountbundle.ExportResult, error) {
	s.exportExecuteInput = in
	return accountbundle.ExportResult{Envelope: accountbundle.Envelope{Format: accountbundle.EnvelopeFormat, Version: 1}, AccountCount: 1}, s.err
}

func (s *accountBundleServiceStub) PlanImport(_ context.Context, in accountbundle.ImportPlanInput) (accountbundle.ImportPlan, error) {
	s.importPlanInput = in
	return accountbundle.ImportPlan{BundleHash: strings.Repeat("b", 64), Ready: 1}, s.err
}

func (s *accountBundleServiceStub) ExecuteImport(_ context.Context, in accountbundle.ImportExecuteInput) (accountbundle.ImportExecutionResult, error) {
	s.importExecuteInput = in
	return accountbundle.ImportExecutionResult{BundleHash: in.BundleHash, Completed: 1}, s.err
}

func TestAccountBundleRoutesKeepSecretsOutOfResponses(t *testing.T) {
	service := &accountBundleServiceStub{}
	router := accountBundleTestRouter(7, service)
	secret := "bundle-password-secret"
	body := `{"tenant_id":7,"account_ids":[11],"plan_hash":"` + strings.Repeat("a", 64) + `","password":"` + secret + `","confirmation":"EXPORT_ENCRYPTED_ACCOUNT_BUNDLE","reason":"迁移"}`
	rec := doAccountIntakeRequest(router, "/admin/v1/credentials/account-bundles/export/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.exportExecuteInput.Password != secret || service.exportExecuteInput.ActorID != "admin_token:9" {
		t.Fatalf("execute input=%+v", service.exportExecuteInput)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("响应泄露迁移包密码：%s", rec.Body.String())
	}
}

func TestAccountBundleRoutesRejectCrossTenantAndMissingService(t *testing.T) {
	service := &accountBundleServiceStub{}
	router := accountBundleTestRouter(8, service)
	rec := doAccountIntakeRequest(router, "/admin/v1/credentials/account-bundles/export/plan", `{"tenant_id":7,"account_ids":[11]}`)
	if rec.Code != http.StatusForbidden || service.exportPlanInput.TenantID != 0 {
		t.Fatalf("cross tenant status=%d input=%+v body=%s", rec.Code, service.exportPlanInput, rec.Body.String())
	}

	router = accountBundleTestRouter(7, nil)
	rec = doAccountIntakeRequest(router, "/admin/v1/credentials/account-bundles/export/plan", `{"tenant_id":7,"account_ids":[11]}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "account_bundle_not_configured") {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAccountBundleHandlerForwardsActorScopeTenantID 咬住 handler 把认证解析出的自有租户
// (ident.ScopeTenantID)透传进服务层输入——服务层纵深第二锁据此判越权。变异:删掉
// account_bundle_handler.go 构造里的 ActorScopeTenantID: ident.ScopeTenantID,本测试转红
// (scope 变 0,合法导出会被服务层 ErrForbidden 误拒,透传断言先失败)。
func TestAccountBundleHandlerForwardsActorScopeTenantID(t *testing.T) {
	service := &accountBundleServiceStub{}
	router := accountBundleTestRouter(7, service)
	rec := doAccountIntakeRequest(router, "/admin/v1/credentials/account-bundles/export/plan", `{"tenant_id":7,"account_ids":[11]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法同租户导出预检 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.exportPlanInput.ActorScopeTenantID != 7 {
		t.Fatalf("handler 未透传自有租户到服务层:ActorScopeTenantID=%d，期望 7", service.exportPlanInput.ActorScopeTenantID)
	}
}

func accountBundleTestRouter(tenantID int64, service *accountBundleServiceStub) http.Handler {
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth:    accountIntakeAuthStub{identity: tenantTokenIdentity(tenantID)},
			Service: &accountIntakeServiceStub{}, Capabilities: allowAccountIntakeCapability{},
			BundleService: service,
		})
	})
	return router
}
