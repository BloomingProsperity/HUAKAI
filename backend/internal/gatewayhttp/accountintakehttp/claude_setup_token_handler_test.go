package accountintakehttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

func TestClaudeSetupTokenPlan强制专用模式(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{
		PlanHash: strings.Repeat("a", 64),
		Plan:     intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceClaudeSetupToken},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"content":"setup-secret","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"api_key"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-setup-token/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.planInput.SourceKind != intake.SourceClaudeSetupToken ||
		service.planInput.DefaultVendor != credentialstore.VendorAnthropic ||
		service.planInput.DefaultAuthMode != credentialstore.AuthModeClaudeSetupToken ||
		service.planInput.Account.AccountType != "oauth" {
		t.Fatalf("plan input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), "setup-secret") {
		t.Fatalf("响应泄漏 Setup Token：%s", rec.Body.String())
	}

	for _, override := range []string{
		`,"source_kind":"json_import"`,
		`,"default_vendor":"openai"`,
		`,"default_auth_mode":"api_key"`,
	} {
		invalid := strings.TrimSuffix(body, "}") + override + "}"
		rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-setup-token/plan", invalid)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("override=%s status=%d body=%s", override, rec.Code, rec.Body.String())
		}
	}
}

func TestClaudeSetupTokenExecute传递日志身份且不回显秘密(t *testing.T) {
	service := &accountIntakeServiceStub{executeResult: accountintake.ExecutionResult{
		PlanHash: strings.Repeat("b", 64),
		Summary:  accountintake.ExecutionSummary{Created: 1},
		Items:    []accountintake.ExecutionItem{{Status: accountintake.StatusCreated, ProviderAccountID: 81}},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	secret := "setup-secret-sentinel"
	body := `{"tenant_id":7,"content":"` + secret + `","account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"},"plan_hash":"` + strings.Repeat("b", 64) + `","confirmations":["confirm_weak_identity"],"reason":"导入长期令牌"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/claude-setup-token/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.ActorID == "" || service.executeInput.ActorRole != "tenant_operator" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("执行响应泄漏 Setup Token：%s", rec.Body.String())
	}
}
