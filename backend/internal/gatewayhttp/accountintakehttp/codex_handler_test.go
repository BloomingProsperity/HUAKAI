package accountintakehttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

func TestCodexAccountIntakePlanForcesDedicatedSourceAndRejectsOverrides(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{
		PlanHash: strings.Repeat("c", 64),
		Plan:     intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceCLI},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"content":"{\"tokens\":{\"access_token\":\"secret-access\"}}","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"oauth"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.planCalls != 1 || service.planInput.SourceKind != intake.SourceCLI ||
		service.planInput.DefaultVendor != credentialstore.VendorOpenAI ||
		service.planInput.DefaultAuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("plan input=%+v calls=%d", service.planInput, service.planCalls)
	}
	if strings.Contains(rec.Body.String(), "secret-access") {
		t.Fatalf("响应泄漏 Codex token：%s", rec.Body.String())
	}

	for _, override := range []string{
		`,"source_kind":"json_import"`,
		`,"default_vendor":"anthropic"`,
		`,"default_auth_mode":"api_key"`,
	} {
		invalid := strings.TrimSuffix(body, "}") + override + "}"
		rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex/plan", invalid)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("override=%s status=%d body=%s", override, rec.Code, rec.Body.String())
		}
	}
	if service.planCalls != 1 {
		t.Fatalf("非法覆盖不应进入 service，calls=%d", service.planCalls)
	}
}

func TestCodexAccountIntakeExecutePassesAuditIdentityWithoutEchoingSecret(t *testing.T) {
	service := &accountIntakeServiceStub{executeResult: accountintake.ExecutionResult{
		PlanHash: strings.Repeat("d", 64),
		Summary:  accountintake.ExecutionSummary{Created: 1},
		Items:    []accountintake.ExecutionItem{{Status: accountintake.StatusCreated, ProviderAccountID: 81, AccountCredentialID: 91}},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	secret := "codex-secret-sentinel"
	body := `{"tenant_id":7,"content":"` + secret + `","account":{"provider_id":2,"channel_id":3,"name_prefix":"codex","account_type":"oauth"},"plan_hash":"` + strings.Repeat("d", 64) + `","confirmations":["confirm_weak_identity"],"reason":"导入 Codex 账号"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.TenantID != 7 || service.executeInput.ActorRole != "tenant_operator" || service.executeInput.ActorID == "" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("执行响应泄漏 Codex token：%s", rec.Body.String())
	}
}
