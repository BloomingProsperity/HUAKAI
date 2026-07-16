package intake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestBuildCodexPlanDistinguishesCreateUpdateAndDuplicate(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	content := `[
		{"account_id":"acct-new","chatgpt_user_id":"user-new","email":"new@example.com","access_token":"access-new","refresh_token":"refresh-new","expires_at":"2026-07-17T10:00:00Z"},
		{"account_id":"acct-existing","chatgpt_user_id":"user-existing","email":"existing@example.com","access_token":"access-existing","refresh_token":"refresh-existing"},
		{"account_id":"acct-new","chatgpt_user_id":"user-new","email":"new@example.com","access_token":"different-token","refresh_token":"different-refresh"},
		{"account_id":"acct-other","chatgpt_user_id":"user-other","email":"collision@example.com","access_token":"access-conflict","refresh_token":"refresh-conflict"}
	]`
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    content,
		Now:        now,
		Existing: []ExistingCredential{
			{
				ProviderAccountID: 10, ProviderAccountName: "codex-existing",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "acct-existing", ExternalAccountEmail: "existing@example.com", ExternalSubjectID: "user-existing",
			},
			{
				ProviderAccountID: 20, ProviderAccountName: "codex-collision",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "acct-original", ExternalAccountEmail: "collision@example.com",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContractVersion != ContractVersion || plan.InputCount != 4 {
		t.Fatalf("plan header=%+v", plan)
	}
	wantActions := []Action{ActionCreate, ActionUpdate, ActionSkip, ActionCreate}
	wantCodes := []string{"create_account", "rotate_existing_credential", "duplicate_identity", "create_account"}
	for index := range wantActions {
		if plan.Items[index].Action != wantActions[index] || plan.Items[index].Code != wantCodes[index] {
			t.Fatalf("item[%d]=%+v want action/code=%s/%s", index, plan.Items[index], wantActions[index], wantCodes[index])
		}
	}
	if plan.Items[1].ExistingAccountID != 10 {
		t.Fatalf("existing account mapping lost: %+v", plan.Items)
	}
	if plan.Summary != (Summary{Create: 2, Update: 1, Skip: 1}) {
		t.Fatalf("summary=%+v", plan.Summary)
	}
}

func TestBuildWeakIdentityRequiresConfirmationAndNeverEchoesSecret(t *testing.T) {
	secret := "session-super-secret-value"
	plan, err := Build(BuildInput{SourceKind: SourceCodexCLI, Content: secret})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items=%d", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Action != ActionCreate || item.Identity.Strength != "opaque" {
		t.Fatalf("item=%+v", item)
	}
	if !contains(item.Warnings, "weak_identity") || !contains(item.RequiredConfirmations, "confirm_weak_identity") {
		t.Fatalf("weak identity controls missing: %+v", item)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("plan leaked source secret: %s", raw)
	}
}

func TestBuildExpiredWithoutRefreshFails(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceJSON,
		Content: `{"vendor":"openai","auth_mode":"codex_cli_oauth","account_id":"acct-expired",
			"access_token":"expired-access","expires_at":"2026-07-15T00:00:00Z"}`,
		Now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Items[0]; got.Action != ActionFail || got.Code != "expired_without_refresh" {
		t.Fatalf("item=%+v", got)
	}
}

func TestBuildFailedCandidateDoesNotHideLaterValidIdentity(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content: `[
			{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"expired","expires_at":"2026-07-15T00:00:00Z"},
			{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"valid","refresh_token":"refresh-valid","expires_at":"2026-07-17T00:00:00Z"}
		]`,
		Now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first := plan.Items[0]; first.Action != ActionFail || first.Code != "expired_without_refresh" {
		t.Fatalf("first=%+v", first)
	}
	if second := plan.Items[1]; second.Action != ActionCreate || second.Code != "create_account" {
		t.Fatalf("second=%+v，失败项不得占用身份去重键", second)
	}
	if plan.Summary.Fail != 1 || plan.Summary.Create != 1 || plan.Summary.Skip != 0 {
		t.Fatalf("summary=%+v", plan.Summary)
	}
}

func TestBuildDisabledSourcesFailClosed(t *testing.T) {
	for _, source := range []SourceKind{
		SourceClaudeCookie, SourceSetupToken, SourceAgentIdentity, SourceRemoteSync, SourceAccountBundle,
	} {
		if _, err := Build(BuildInput{SourceKind: source, Content: "secret"}); err == nil || !strings.Contains(err.Error(), string(source)) {
			t.Fatalf("source=%s err=%v", source, err)
		}
	}
}

func TestBuildMalformedJSONDoesNotBecomeRawToken(t *testing.T) {
	if _, err := Build(BuildInput{SourceKind: SourceCodexCLI, Content: `{"access_token":"broken"`}); err == nil {
		t.Fatal("畸形 JSON 不得被当成 raw token")
	}
}

func TestBuildEmailOnlyExistingAccountRequiresExplicitWeakMatchConfirmation(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"email":"same@example.com","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 33, ProviderAccountName: "email-only",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			ExternalAccountEmail: "same@example.com",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Action != ActionUpdate || item.Code != "rotate_email_only_credential" || item.ExistingAccountID != 33 {
		t.Fatalf("item=%+v，双方都只有同一邮箱时应形成受控弱匹配", item)
	}
	if !contains(item.RequiredConfirmations, "confirm_weak_identity_match") || !contains(item.Warnings, "weak_identity_match") {
		t.Fatalf("item=%+v，弱匹配必须显式确认", item)
	}
}

func TestBuildEmailOnlyImportConflictsWithStrongExistingIdentity(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"email":"same@example.com","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 34, ProviderAccountName: "strong-existing",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			ExternalAccountID: "workspace-a", ExternalSubjectID: "user-a", ExternalAccountEmail: "same@example.com",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Action != ActionConflict || item.Code != "weak_identity_matches_strong_account" {
		t.Fatalf("item=%+v，只有邮箱的导入不能覆盖强身份账号", item)
	}
}

func TestBuildDuplicateExistingIdentityIsAmbiguous(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"acct-duplicate","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{
			{
				ProviderAccountID: 41, ProviderAccountName: "duplicate-a",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "acct-duplicate",
			},
			{
				ProviderAccountID: 42, ProviderAccountName: "duplicate-b",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "acct-duplicate",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Action != ActionConflict || item.Code != "account_scope_ambiguous" {
		t.Fatalf("item=%+v，重复身份不得任选一条更新", item)
	}
}

func TestBuildSameWorkspaceDifferentSubjectsCreatesSeparateAccounts(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content: `[
			{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-a","refresh_token":"refresh-a"},
			{"account_id":"workspace-1","chatgpt_user_id":"user-b","access_token":"access-b","refresh_token":"refresh-b"}
		]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Create != 2 || plan.Summary.Skip != 0 || plan.Summary.Conflict != 0 {
		t.Fatalf("summary=%+v，同一工作区不同成员必须允许独立建号", plan.Summary)
	}
}

func TestBuildSameWorkspaceDifferentSubjectDoesNotUpdateExistingMember(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"workspace-1","chatgpt_user_id":"user-b","access_token":"access-b","refresh_token":"refresh-b"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 50, ProviderAccountName: "workspace-member-a",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionCreate || item.ExistingAccountID != 0 {
		t.Fatalf("item=%+v，同一工作区的不同个人身份不得覆盖已有成员", item)
	}
}

func TestBuildDuplicateExistingSubjectIsAmbiguous(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{
			{
				ProviderAccountID: 51, ProviderAccountName: "subject-a",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			},
			{
				ProviderAccountID: 52, ProviderAccountName: "subject-b",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionConflict || item.Code != "subject_identity_ambiguous" {
		t.Fatalf("item=%+v，同一个人身份命中多条时必须人工消歧", item)
	}
}

func TestBuildAccessOnlyDifferentFingerprintDoesNotMergeBySubject(t *testing.T) {
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-new"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 60, ProviderAccountName: "access-only-old",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			CredentialFingerprint: fingerprintForContent(t,
				`{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-old"}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionCreate || item.ExistingAccountID != 0 {
		t.Fatalf("item=%+v，仅访问凭据的不同指纹不得按共享身份误合并", item)
	}
}

func TestBuildAccessOnlyUsesCredentialFingerprint(t *testing.T) {
	content := `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-only"}`
	plan, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    content,
		Existing: []ExistingCredential{{
			ProviderAccountID: 61, ProviderAccountName: "access-only",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			CredentialFingerprint: fingerprintForContent(t, content),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionUpdate || item.ExistingAccountID != 61 {
		t.Fatalf("item=%+v，只有访问凭据时必须按凭据指纹命中", item)
	}
}

func fingerprintForContent(t *testing.T, content string) string {
	t.Helper()
	candidates, err := parseSource(SourceCodexCLI, content, "", "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("parse source: candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
	sum := sha256.Sum256(append([]byte(credentialstore.ModeKey(candidate.Vendor, candidate.AuthMode)+"\x00"), candidate.Payload...))
	return hex.EncodeToString(sum[:])
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
