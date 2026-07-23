package accountintake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

type intakeCompatibilityLookup struct {
	account admindb.AdminProviderAccountRow
	family  string
	err     error
}

func (s intakeCompatibilityLookup) GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	if s.err != nil {
		return admindb.AdminProviderAccountRow{}, s.err
	}
	return s.account, nil
}

func (s intakeCompatibilityLookup) GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.family, nil
}

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

func TestEnrichUpdateCompatibilityRejectsWrongExistingAccount(t *testing.T) {
	plan := intake.Plan{
		ContractVersion: intake.ContractVersion,
		Items: []intake.Item{{
			Index: 0, Action: intake.ActionUpdate, ExistingAccountID: 77,
			FieldChanges: []string{"credential"}, RequiredConfirmations: []string{"confirm_credential_rotation"},
		}},
	}
	candidates := []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
	}}
	err := enrichUpdateCompatibility(context.Background(), intakeCompatibilityLookup{
		account: admindb.AdminProviderAccountRow{ProviderID: 12, AccountType: "api_key"},
		family:  "anthropic_messages",
	}, 7, &plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Action != intake.ActionFail || item.Code != "provider_protocol_incompatible" ||
		len(item.FieldChanges) != 0 || len(item.RequiredConfirmations) != 0 ||
		plan.Summary.Fail != 1 || plan.Summary.Update != 0 {
		t.Fatalf("item=%+v summary=%+v，期望不兼容更新明确失败", item, plan.Summary)
	}
}

func TestEnrichUpdateCompatibilityFailsClosedOnLookupError(t *testing.T) {
	plan := intake.Plan{Items: []intake.Item{{Index: 0, Action: intake.ActionUpdate, ExistingAccountID: 77}}}
	candidates := []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
	}}
	want := errors.New("数据库不可用")
	err := enrichUpdateCompatibility(context.Background(), intakeCompatibilityLookup{err: want}, 7, &plan, candidates)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v，期望保留查询失败并阻止预检", err)
	}
	if plan.Items[0].Action != intake.ActionUpdate {
		t.Fatalf("查询失败时不应伪造业务结论：%+v", plan.Items[0])
	}
}

func TestEnrichUpdateCompatibilityKeepsCompatibleAccount(t *testing.T) {
	plan := intake.Plan{Items: []intake.Item{{Index: 0, Action: intake.ActionUpdate, ExistingAccountID: 77}}}
	candidates := []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
	}}
	err := enrichUpdateCompatibility(context.Background(), intakeCompatibilityLookup{
		account: admindb.AdminProviderAccountRow{ProviderID: 12, AccountType: "oauth"},
		family:  "openai_codex",
	}, 7, &plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Action != intake.ActionUpdate || plan.Summary.Update != 1 {
		t.Fatalf("兼容账号应保持更新：item=%+v summary=%+v", plan.Items[0], plan.Summary)
	}
	if err := accountcreate.ValidateProtocolCompatibility(
		"openai_codex", "oauth", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth,
	); err != nil {
		t.Fatalf("测试前提失效：%v", err)
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

func TestExecutionErrorClassification保留暂存流程原因(t *testing.T) {
	tests := []struct {
		err     error
		code    string
		message string
	}{
		{ErrStagedCredentialNotFound, "credential_flow_not_found", "短期账号流程不存在"},
		{ErrStagedCredentialExpired, "credential_flow_expired", "短期账号流程已过期"},
		{ErrStagedCredentialReplay, "credential_flow_replayed", "短期账号流程已被领取"},
		{ErrPlanChanged, "account_plan_changed", "账号状态已变化，需要重新预检"},
	}
	for _, test := range tests {
		if code := executionErrorCode(test.err); code != test.code {
			t.Fatalf("err=%v code=%q，期望 %q", test.err, code, test.code)
		}
		if message := executionErrorMessage(test.err); message != test.message {
			t.Fatalf("err=%v message=%q，期望 %q", test.err, message, test.message)
		}
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
