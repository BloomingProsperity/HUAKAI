package intake

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestBuildCandidatesRejectsAmbiguousExistingIdentity(t *testing.T) {
	candidate := oauthCandidate("workspace-1", "person@example.com", `{"refresh_token":"refresh-new"}`)
	plan := BuildCandidates(BuildInput{
		TenantID: 7, SourceKind: SourceJSON, Existing: []ExistingCredential{
			existingCredential(101, 1001, credentialstore.StateActive, "workspace-1"),
			existingCredential(202, 2002, credentialstore.StateActive, "workspace-1"),
		},
		Now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	}, []credentialacq.CredentialCandidate{candidate}).Plan

	item := requireSingleItem(t, plan)
	if item.Action != ActionConflict || item.Code != "account_scope_ambiguous" {
		t.Fatalf("item=%+v，期望同一身份命中多账号时显式冲突", item)
	}
}

func TestBuildCandidatesRejectsDuplicateModeInsideOneAccount(t *testing.T) {
	candidate := oauthCandidate("workspace-1", "", `{"refresh_token":"refresh-new"}`)
	first := existingCredential(101, 1001, credentialstore.StateActive, "workspace-1")
	second := existingCredential(102, 1001, credentialstore.StateActive, "workspace-1")
	plan := BuildCandidates(BuildInput{
		TenantID: 7, SourceKind: SourceJSON, Existing: []ExistingCredential{first, second},
	}, []credentialacq.CredentialCandidate{candidate}).Plan

	item := requireSingleItem(t, plan)
	if item.Action != ActionConflict || item.Code != "account_scope_ambiguous" {
		t.Fatalf("item=%+v，期望同账号同模式多凭据时显式冲突", item)
	}
}

func TestBuildCandidatesDeduplicatesBatchAndRedactsIdentity(t *testing.T) {
	candidate := oauthCandidate("workspace-secret", "person@example.com", `{"refresh_token":"refresh-secret"}`)
	plan := BuildCandidates(BuildInput{
		TenantID: 7, SourceKind: SourceJSON,
	}, []credentialacq.CredentialCandidate{candidate, candidate}).Plan

	if len(plan.Items) != 2 || plan.Items[0].Action != ActionCreate ||
		plan.Items[1].Action != ActionSkip || plan.Items[1].Code != "duplicate_input" {
		t.Fatalf("plan=%+v，期望首项创建、重复项跳过", plan)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"workspace-secret", "person@example.com", "refresh-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("预检响应泄露原始身份或凭据材料 %q：%s", secret, text)
		}
	}
	if !strings.HasPrefix(plan.Items[0].Identity.ExternalAccountID, "account_") ||
		!strings.HasPrefix(plan.Items[0].Identity.ExternalAccountEmail, "email_") {
		t.Fatalf("身份提示未脱敏：%+v", plan.Items[0].Identity)
	}
}

func TestBuildCandidatesAppliesCredentialStateGate(t *testing.T) {
	tests := []struct {
		name             string
		state            string
		wantAction       Action
		wantCode         string
		wantConfirmation string
	}{
		{name: "可恢复过期", state: credentialstore.StateExpired, wantAction: ActionUpdate, wantCode: "rotate_account_scope_credential", wantConfirmation: "confirm_credential_recovery"},
		{name: "刷新中", state: credentialstore.StateRefreshing, wantAction: ActionConflict, wantCode: "credential_refresh_in_progress"},
		{name: "已撤销", state: credentialstore.StateRevoked, wantAction: ActionConflict, wantCode: "credential_revoked_requires_explicit_reactivation"},
		{name: "人工处理", state: credentialstore.StateOperatorAttention, wantAction: ActionConflict, wantCode: "credential_operator_attention_requires_resolution"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := BuildCandidates(BuildInput{
				TenantID: 7, SourceKind: SourceJSON,
				Existing: []ExistingCredential{existingCredential(101, 1001, tt.state, "workspace-1")},
			}, []credentialacq.CredentialCandidate{
				oauthCandidate("workspace-1", "", `{"refresh_token":"refresh-new"}`),
			}).Plan
			item := requireSingleItem(t, plan)
			if item.Action != tt.wantAction || item.Code != tt.wantCode {
				t.Fatalf("item=%+v，期望 action=%s code=%s", item, tt.wantAction, tt.wantCode)
			}
			if tt.wantConfirmation != "" && !contains(item.RequiredConfirmations, tt.wantConfirmation) {
				t.Fatalf("confirmations=%v，缺少 %s", item.RequiredConfirmations, tt.wantConfirmation)
			}
		})
	}
}

func TestBuildCandidatesMatchesOpaqueAPIKeyOnlyByFingerprint(t *testing.T) {
	payload := []byte(`{"api_key":"sk-stable-material"}`)
	fingerprint := credentialstore.CredentialMaterialFingerprint(
		7, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, payload,
	)
	plan := BuildCandidates(BuildInput{
		TenantID: 7, SourceKind: SourceJSON,
		Existing: []ExistingCredential{{
			CredentialID: 11, CredentialVersion: 3, ProviderAccountID: 101,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
			State: credentialstore.StateActive, ExternalAccountID: "unrelated",
			CredentialFingerprint: fingerprint,
		}},
	}, []credentialacq.CredentialCandidate{{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
		Payload: payload, ExternalAccountID: "claimed-account",
		AccountIDSource: accountident.SourceImportPayload,
	}}).Plan

	item := requireSingleItem(t, plan)
	if item.Action != ActionUpdate || item.ExistingAccountID != 101 || item.ExistingCredentialID != 11 {
		t.Fatalf("item=%+v，期望仅按稳定材料指纹命中已有 API key", item)
	}
}

func TestBuildClaudeSetupTokenPlanIsStaticRedactedAndDeduplicated(t *testing.T) {
	content := "setup-token-secret"
	first, err := Build(BuildInput{TenantID: 7, SourceKind: SourceClaudeSetupToken, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	item := requireSingleItem(t, first.Plan)
	if item.Vendor != credentialstore.VendorAnthropic || item.AuthMode != credentialstore.AuthModeClaudeSetupToken ||
		item.Action != ActionCreate || item.Lifecycle.Refreshable || item.Lifecycle.HasRefreshMaterial {
		t.Fatalf("item=%+v", item)
	}
	raw, _ := json.Marshal(first.Plan)
	if strings.Contains(string(raw), content) {
		t.Fatalf("plan 泄漏 setup token: %s", raw)
	}
	fingerprint := credentialstore.CredentialMaterialFingerprint(7, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, first.Candidates[0].Payload)
	second, err := Build(BuildInput{
		TenantID: 7, SourceKind: SourceClaudeSetupToken, Content: content,
		Existing: []ExistingCredential{{
			CredentialID: 11, CredentialVersion: 2, ProviderAccountID: 101,
			Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeSetupToken,
			State: credentialstore.StateActive, CredentialFingerprint: fingerprint,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	matched := requireSingleItem(t, second.Plan)
	if matched.Action != ActionUpdate || matched.ExistingAccountID != 101 || matched.ExistingCredentialID != 11 {
		t.Fatalf("matched=%+v", matched)
	}
}

func TestBuildCLIPlanUsesStrictCodexParser(t *testing.T) {
	built, err := Build(BuildInput{
		TenantID: 7, SourceKind: SourceCLI,
		Content: `{"vendor":"anthropic","auth_mode":"chatgpt","access_token":"codex-access","oauth_token_endpoint":"https://attacker.test/token"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Candidates) != 1 || len(built.Plan.Items) != 1 {
		t.Fatalf("candidate/item count=%d/%d", len(built.Candidates), len(built.Plan.Items))
	}
	candidate := built.Candidates[0]
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("mode=%s/%s", candidate.Vendor, candidate.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["oauth_token_endpoint"]; exists {
		t.Fatalf("专用 Codex 解析器保留了输入 endpoint：%v", payload)
	}
	if built.Plan.Items[0].Vendor != credentialstore.VendorOpenAI || built.Plan.Items[0].AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("plan item=%+v", built.Plan.Items[0])
	}
}

func TestBuildCandidatesExposesSubscriptionLabelWithoutIdentityLeak(t *testing.T) {
	candidate := oauthCandidate("workspace-secret", "person@example.com", `{
		"refresh_token":"refresh-secret",
		"chatgpt_plan_type":"pro",
		"external_subject_id":"subject-secret"
	}`)
	prepared := BuildCandidates(BuildInput{
		TenantID: 7, SourceKind: SourceJSON,
	}, []credentialacq.CredentialCandidate{candidate})
	item := requireSingleItem(t, prepared.Plan)
	if prepared.Plan.ContractVersion != "account-intake-v2" {
		t.Fatalf("contract_version=%s", prepared.Plan.ContractVersion)
	}
	if item.Subscription == nil || item.Subscription.Plan != "pro" || item.Subscription.Status != "observed" ||
		len(item.SystemLabels) != 1 || item.SystemLabels[0] != "openai:pro" {
		t.Fatalf("套餐预览不完整：%+v", item)
	}
	if item.Subscription.SubjectRef != "" || item.Subscription.WorkspaceRef != "" {
		t.Fatalf("预检响应泄露上游身份：%+v", item.Subscription)
	}
	if prepared.Candidates[0].Subscription.SubjectRef != "subject-secret" ||
		prepared.Candidates[0].Subscription.WorkspaceRef != "" {
		t.Fatalf("内部候选项没有保留套餐归属证据：%+v", prepared.Candidates[0].Subscription)
	}
	if !contains(item.FieldChanges, "subscription_profile") {
		t.Fatalf("field_changes=%v，缺少套餐投影", item.FieldChanges)
	}
	raw, err := json.Marshal(prepared.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"workspace-secret", "subject-secret", "person@example.com", "refresh-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("套餐预检泄漏 %q：%s", secret, raw)
		}
	}
}

func oauthCandidate(accountID, email, payload string) credentialacq.CredentialCandidate {
	return credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(payload), ExternalAccountID: accountID, ExternalAccountEmail: email,
		AccountIDSource: accountident.SourceImportPayload,
	}
}

func existingCredential(credentialID, accountID int64, state, externalAccountID string) ExistingCredential {
	return ExistingCredential{
		CredentialID: credentialID, CredentialVersion: 3,
		ProviderAccountID: accountID, ProviderAccountName: "account",
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		State: state, ExternalAccountID: externalAccountID,
		AccountIDSource: accountident.SourceImportPayload,
	}
}

func requireSingleItem(t *testing.T, plan Plan) Item {
	t.Helper()
	if len(plan.Items) != 1 {
		t.Fatalf("items=%d，期望 1", len(plan.Items))
	}
	return plan.Items[0]
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
