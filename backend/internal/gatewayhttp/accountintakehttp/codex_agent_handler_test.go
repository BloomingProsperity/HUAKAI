package accountintakehttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

func TestCodexAgentPlanForcesDedicatedModeAndSessionAccount(t *testing.T) {
	service := &accountIntakeServiceStub{planResult: accountintake.PlanResult{
		PlanHash: strings.Repeat("a", 64),
		Plan:     intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceCodexAgent},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"content":"agent-secret","account":{"provider_id":2,"channel_id":3,"name_prefix":"agent","account_type":"api_key"}}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex-agent/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := service.planInput
	if got.SourceKind != intake.SourceCodexAgent || got.DefaultVendor != credentialstore.VendorOpenAI ||
		got.DefaultAuthMode != credentialstore.AuthModeCodexAgent || got.Account.AccountType != "session" {
		t.Fatalf("plan input=%+v", got)
	}
	if strings.Contains(rec.Body.String(), "agent-secret") {
		t.Fatalf("响应泄漏 Agent Identity 材料：%s", rec.Body.String())
	}
	for _, override := range []string{`,"source_kind":"json_import"`, `,"default_vendor":"anthropic"`, `,"default_auth_mode":"api_key"`} {
		invalid := strings.TrimSuffix(body, "}") + override + "}"
		rec = doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex-agent/plan", invalid)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("override=%s status=%d body=%s", override, rec.Code, rec.Body.String())
		}
	}
}

func TestCodexAgentExecuteCarriesAuditIdentityWithoutEchoingMaterial(t *testing.T) {
	service := &accountIntakeServiceStub{executeResult: accountintake.ExecutionResult{
		PlanHash: strings.Repeat("b", 64),
		Items:    []accountintake.ExecutionItem{{Status: accountintake.StatusCreated, ProviderAccountID: 31}},
		Summary:  accountintake.ExecutionSummary{Created: 1},
	}}
	handler := accountIntakeTestHandler(accountIntakeAuthStub{identity: tenantTokenIdentity(7)}, service)
	body := `{"tenant_id":7,"content":"private-key-sentinel","account":{"provider_id":2,"channel_id":3,"name_prefix":"agent","account_type":"session"},"plan_hash":"` + strings.Repeat("b", 64) + `","reason":"导入 Agent Identity"}`
	rec := doAccountIntakeRequest(handler, "/admin/v1/credentials/account-imports/codex-agent/execute", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.executeCalls != 1 || service.executeInput.ActorID == "" || service.executeInput.ActorRole != "tenant_operator" {
		t.Fatalf("execute input=%+v calls=%d", service.executeInput, service.executeCalls)
	}
	if strings.Contains(rec.Body.String(), "private-key-sentinel") {
		t.Fatalf("响应泄漏 Agent Identity 材料：%s", rec.Body.String())
	}
}
