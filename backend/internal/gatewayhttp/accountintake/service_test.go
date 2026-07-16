package accountintake

import (
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

func TestEnrichPlanRequiresMixedChannelConfirmation(t *testing.T) {
	plan := intake.Plan{
		ContractVersion: intake.ContractVersion,
		Items: []intake.Item{{
			Index: 0, Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
			Action: intake.ActionCreate,
		}},
	}
	candidates := []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
	}}
	enrichPlan(&plan, candidates, "openai_chat", AccountDefaults{
		ProviderID: 2, ChannelID: 3, AccountType: "api_key",
	}, []mixedchannelrisk.Account{{
		ID: 99, ProviderID: 8, ChannelID: 3, AccountType: "oauth",
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
	}})

	item := plan.Items[0]
	if item.Action != intake.ActionCreate || item.MixedChannelRisk == nil || !item.MixedChannelRisk.HighRisk {
		t.Fatalf("item=%+v，期望保留 create 并附高风险报告", item)
	}
	if !containsString(item.RequiredConfirmations, "confirm_mixed_channel_risk") ||
		plan.Summary.Create != 1 {
		t.Fatalf("item=%+v summary=%+v，缺少混用风险确认", item, plan.Summary)
	}
}

func TestEnrichPlanFailsProtocolMismatch(t *testing.T) {
	plan := intake.Plan{
		ContractVersion: intake.ContractVersion,
		Items: []intake.Item{{
			Index: 0, Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
			Action: intake.ActionCreate, FieldChanges: []string{"provider_account"},
		}},
	}
	enrichPlan(&plan, []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
	}}, "anthropic_messages", AccountDefaults{
		ProviderID: 2, ChannelID: 3, AccountType: "api_key",
	}, nil)

	item := plan.Items[0]
	if item.Action != intake.ActionFail || item.Code != "provider_protocol_incompatible" ||
		plan.Summary.Fail != 1 || plan.Summary.Create != 0 {
		t.Fatalf("item=%+v summary=%+v，期望协议不兼容 fail", item, plan.Summary)
	}
}

func TestPlanHashBindsContentDefaultsAndPlan(t *testing.T) {
	base := PlanInput{
		TenantID: 7, SourceKind: intake.SourceJSON,
		DefaultVendor:   credentialstore.VendorOpenAI,
		DefaultAuthMode: credentialstore.AuthModeAPIKey,
		Content:         `{"api_key":"sk-one"}`,
		Account:         AccountDefaults{ProviderID: 2, ChannelID: 3, NamePrefix: "acct", AccountType: "api_key"},
		Now:             time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	}
	plan := intake.Plan{ContractVersion: intake.ContractVersion, SourceKind: intake.SourceJSON}
	first, err := planHash(base, plan)
	if err != nil {
		t.Fatal(err)
	}
	changedContent := base
	changedContent.Content = `{"api_key":"sk-two"}`
	second, _ := planHash(changedContent, plan)
	changedDefaults := base
	changedDefaults.Account.ChannelID = 4
	third, _ := planHash(changedDefaults, plan)
	changedPlan := plan
	changedPlan.Summary.Create = 1
	fourth, _ := planHash(base, changedPlan)
	if first == second || first == third || first == fourth || !validPlanHash(first) {
		t.Fatalf("hashes first=%s content=%s defaults=%s plan=%s", first, second, third, fourth)
	}
}

func TestValidateConfirmationsAndPlanHash(t *testing.T) {
	if err := validateConfirmations([]string{"confirm_credential_rotation", "confirm_weak_identity"}); err != nil {
		t.Fatalf("合法确认项被拒：%v", err)
	}
	if err := validateConfirmations([]string{"approve_everything"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("未知确认项 err=%v，期望 ErrInvalidInput", err)
	}
	for _, invalid := range []string{"", "ABC", "0123"} {
		if validPlanHash(invalid) {
			t.Fatalf("非法 plan_hash 被接受：%q", invalid)
		}
	}
}

func TestExecutionErrorMessageDoesNotExposeBackendDetails(t *testing.T) {
	message := executionErrorMessage(errors.New("pq: duplicate key value contains secret row details"))
	if message != "该项写入失败，事务已回滚且未留下部分数据" {
		t.Fatalf("message=%q，期望稳定且不含底层错误", message)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
