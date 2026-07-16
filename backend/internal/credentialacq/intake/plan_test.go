package intake

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestBuildCodexPlanDistinguishesCreateConflictAndDuplicate(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	content := `[
		{"account_id":"acct-new","chatgpt_user_id":"user-new","email":"new@example.com","access_token":"access-new","refresh_token":"refresh-new","expires_at":"2026-07-17T10:00:00Z"},
		{"account_id":"acct-existing","chatgpt_user_id":"user-existing","email":"existing@example.com","access_token":"access-existing","refresh_token":"refresh-existing"},
		{"account_id":"acct-new","chatgpt_user_id":"user-new","email":"new@example.com","access_token":"different-token","refresh_token":"different-refresh"},
		{"account_id":"acct-other","chatgpt_user_id":"user-other","email":"collision@example.com","access_token":"access-conflict","refresh_token":"refresh-conflict"}
	]`
	plan, err := Build(BuildInput{
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    content,
		Now:        now,
		Existing: []ExistingCredential{
			{
				ProviderAccountID: 10, ProviderAccountName: "codex-existing",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "acct-existing", ExternalAccountEmail: "existing@example.com", ExternalSubjectID: "user-existing",
			},
			{
				ProviderAccountID: 20, ProviderAccountName: "codex-collision",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
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
	wantActions := []Action{ActionCreate, ActionConflict, ActionConflict, ActionCreate}
	wantCodes := []string{"create_account", "subject_identity_unverified", "unverified_identity_collision", "create_account"}
	for index := range wantActions {
		if plan.Items[index].Action != wantActions[index] || plan.Items[index].Code != wantCodes[index] {
			t.Fatalf("item[%d]=%+v want action/code=%s/%s", index, plan.Items[index], wantActions[index], wantCodes[index])
		}
	}
	if plan.Items[1].ExistingAccountID != 10 {
		t.Fatalf("existing account mapping lost: %+v", plan.Items)
	}
	if plan.Summary != (Summary{Create: 2, Conflict: 2}) {
		t.Fatalf("summary=%+v", plan.Summary)
	}
}

func TestBuildAPIKeyMatchesPersistedMaterialFingerprint(t *testing.T) {
	content := `{"vendor":"openai","auth_mode":"api_key","api_key":"sk-same","label":"import"}`
	fingerprint := credentialstore.CredentialMaterialFingerprint(
		7, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey,
		[]byte(`{"api_key":"sk-same","label":"stored"}`),
	)
	plan, err := Build(BuildInput{
		TenantID: 7, SourceKind: SourceJSON, Content: content,
		Existing: []ExistingCredential{{
			ProviderAccountID: 90, ProviderAccountName: "official-key",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
			State:                 credentialstore.StateActive,
			CredentialFingerprint: fingerprint,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionUpdate ||
		item.Code != "rotate_existing_credential" || item.ExistingAccountID != 90 {
		t.Fatalf("item=%+v，同一 API key 必须命中已有账号", item)
	}
}

func TestBuildUnverifiedSubjectCollisionRequiresManualResolution(t *testing.T) {
	plan, err := Build(BuildInput{
		TenantID: 7, SourceKind: SourceCodexCLI,
		Content: `[
			{"account_id":"workspace-a","chatgpt_user_id":"user-a","access_token":"access-a","refresh_token":"refresh-a"},
			{"account_id":"workspace-b","chatgpt_user_id":"user-a","access_token":"access-b","refresh_token":"refresh-b"}
		]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first := plan.Items[0]; first.Action != ActionCreate {
		t.Fatalf("first=%+v", first)
	}
	if second := plan.Items[1]; second.Action != ActionConflict ||
		second.Code != "unverified_identity_collision" ||
		!contains(second.RequiredConfirmations, "resolve_identity_conflict") {
		t.Fatalf("second=%+v，未经验证的同 subject 多凭据必须人工消歧", second)
	}
}

func TestBuildWeakIdentityRequiresConfirmationAndNeverEchoesSecret(t *testing.T) {
	secret := "session-super-secret-value"
	plan, err := Build(BuildInput{TenantID: 7, SourceKind: SourceCodexCLI, Content: secret})
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
		TenantID:   7,
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
		TenantID:   7,
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
		if _, err := Build(BuildInput{TenantID: 7, SourceKind: source, Content: "secret"}); err == nil || !strings.Contains(err.Error(), string(source)) {
			t.Fatalf("source=%s err=%v", source, err)
		}
	}
}

func TestBuildMalformedJSONDoesNotBecomeRawToken(t *testing.T) {
	if _, err := Build(BuildInput{TenantID: 7, SourceKind: SourceCodexCLI, Content: `{"access_token":"broken"`}); err == nil {
		t.Fatal("畸形 JSON 不得被当成 raw token")
	}
}

func TestBuildEmailOnlyExistingAccountRequiresExplicitWeakMatchConfirmation(t *testing.T) {
	plan, err := Build(BuildInput{
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    `{"email":"same@example.com","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 33, ProviderAccountName: "email-only",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:                credentialstore.StateActive,
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
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    `{"email":"same@example.com","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 34, ProviderAccountName: "strong-existing",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
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
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"acct-duplicate","access_token":"new-access","refresh_token":"new-refresh"}`,
		Existing: []ExistingCredential{
			{
				ProviderAccountID: 41, ProviderAccountName: "duplicate-a",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "acct-duplicate",
			},
			{
				ProviderAccountID: 42, ProviderAccountName: "duplicate-b",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
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
		TenantID:   7,
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
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"workspace-1","chatgpt_user_id":"user-b","access_token":"access-b","refresh_token":"refresh-b"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 50, ProviderAccountName: "workspace-member-a",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
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
	item := planTrustedCandidate(t,
		`{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"new-access","refresh_token":"new-refresh"}`,
		[]ExistingCredential{
			{
				ProviderAccountID: 51, ProviderAccountName: "subject-a",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
			{
				ProviderAccountID: 52, ProviderAccountName: "subject-b",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
		},
	)
	if item.Action != ActionConflict || item.Code != "subject_identity_ambiguous" {
		t.Fatalf("item=%+v，同一个人身份命中多条时必须人工消歧", item)
	}
}

// 缺陷：inventory 按账号和 auth_mode 返回多行时，匹配器若按行数判断歧义，
// 同一账号已有两种登录方式会被误报为“多个账号”，无法轮换目标模式。
// 判别变异：恢复 len(matches)>1 的旧判断后，本测试会从 update 变成 conflict。
func TestBuildSameAccountMultipleAuthModesSelectsRequestedMode(t *testing.T) {
	item := planTrustedCandidate(t,
		`{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"new-access","refresh_token":"new-refresh"}`,
		[]ExistingCredential{
			{
				ProviderAccountID: 53, ProviderAccountName: "multi-mode",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
			{
				ProviderAccountID: 53, ProviderAccountName: "multi-mode",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
		},
	)
	if item.Action != ActionUpdate || item.Code != "rotate_existing_credential" || item.ExistingAccountID != 53 {
		t.Fatalf("item=%+v，同一账号多 auth_mode 应选择请求模式", item)
	}
}

func TestBuildSameAccountWithoutRequestedAuthModeIsExplicitConflict(t *testing.T) {
	item := planTrustedCandidate(t,
		`{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"new-access","refresh_token":"new-refresh"}`,
		[]ExistingCredential{
			{
				ProviderAccountID: 54, ProviderAccountName: "other-modes",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
			{
				ProviderAccountID: 54, ProviderAccountName: "other-modes",
				Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexWebOAuth,
				State:             credentialstore.StateActive,
				ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
				AccountIDSource: accountident.SourceChatGPTJWTClaim,
			},
		},
	)
	if item.Action != ActionConflict || item.Code != "identity_mode_conflict" || item.ExistingAccountID != 54 {
		t.Fatalf("item=%+v，没有目标 auth_mode 时必须显式冲突", item)
	}
}

func TestBuildAccessOnlyDifferentFingerprintDoesNotMergeBySubject(t *testing.T) {
	plan, err := Build(BuildInput{
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-new"}`,
		Existing: []ExistingCredential{{
			ProviderAccountID: 60, ProviderAccountName: "access-only-old",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			CredentialFingerprint: fingerprintForContent(t, 7,
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
		TenantID:   7,
		SourceKind: SourceCodexCLI,
		Content:    content,
		Existing: []ExistingCredential{{
			ProviderAccountID: 61, ProviderAccountName: "access-only",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			CredentialFingerprint: fingerprintForContent(t, 7, content),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item := plan.Items[0]; item.Action != ActionUpdate || item.ExistingAccountID != 61 {
		t.Fatalf("item=%+v，只有访问凭据时必须按凭据指纹命中", item)
	}
}

func TestBuildFromInventoryUsesTenantScopedPersistedIdentity(t *testing.T) {
	content := `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-only"}`
	inventory := &identityInventoryStub{rows: []credentialstore.CredentialIdentityMetadata{{
		ProviderAccountID: 71, ProviderAccountName: "persisted-account",
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		State: credentialstore.StateActive, ExternalAccountID: "workspace-1",
		ExternalSubjectID:             "user-a",
		CredentialMaterialFingerprint: fingerprintForContent(t, 7, content),
	}}}
	plan, err := BuildFromInventory(context.Background(), inventory, BuildInput{
		TenantID: 7, SourceKind: SourceCodexCLI, Content: content,
	})
	if err != nil {
		t.Fatalf("BuildFromInventory: %v", err)
	}
	if inventory.tenantID != 7 || inventory.vendor != "" {
		t.Fatalf("inventory scope tenant=%d vendor=%q", inventory.tenantID, inventory.vendor)
	}
	if item := plan.Items[0]; item.Action != ActionUpdate || item.ExistingAccountID != 71 {
		t.Fatalf("item=%+v，未使用持久化指纹命中已有账号", item)
	}
}

func TestBuildFromInventoryKeepsRevokedCredentialAsExplicitConflict(t *testing.T) {
	content := `{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access-only"}`
	inventory := &identityInventoryStub{rows: []credentialstore.CredentialIdentityMetadata{{
		ProviderAccountID: 72, ProviderAccountName: "revoked-account",
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		State: credentialstore.StateRevoked, ExternalAccountID: "workspace-1",
		ExternalSubjectID:             "user-a",
		CredentialMaterialFingerprint: fingerprintForContent(t, 7, content),
	}}}
	plan, err := BuildFromInventory(context.Background(), inventory, BuildInput{
		TenantID: 7, SourceKind: SourceCodexCLI, Content: content,
	})
	if err != nil {
		t.Fatalf("BuildFromInventory: %v", err)
	}
	item := plan.Items[0]
	if item.Action != ActionConflict ||
		item.Code != "credential_revoked_requires_explicit_reactivation" ||
		item.ExistingAccountID != 72 {
		t.Fatalf("item=%+v，revoked 凭据必须保留为显式冲突，不能被过滤后误建账号", item)
	}
}

func TestBuildFromInventoryFailsClosedWhenInventoryUnavailable(t *testing.T) {
	inventory := &identityInventoryStub{err: errors.New("inventory unavailable")}
	_, err := BuildFromInventory(context.Background(), inventory, BuildInput{
		TenantID: 7, SourceKind: SourceCodexCLI, Content: "access-token",
	})
	if !errors.Is(err, ErrIdentityInventoryUnavailable) {
		t.Fatalf("err=%v want ErrIdentityInventoryUnavailable", err)
	}
	if _, err := BuildFromInventory(context.Background(), inventory, BuildInput{
		SourceKind: SourceCodexCLI, Content: "access-token",
	}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("missing tenant err=%v want ErrTenantRequired", err)
	}
}

func TestBuildRequiresTenantBeforeParsingOrMatching(t *testing.T) {
	_, err := Build(BuildInput{
		SourceKind: SourceCodexCLI,
		Content:    `{"access_token":"access-only"}`,
	})
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("err=%v want ErrTenantRequired", err)
	}
}

func TestBuildForgedImportedSubjectCannotSelectExistingAccount(t *testing.T) {
	content := `{"id_token":"eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyLWEiLCJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoid29ya3NwYWNlLTEifX0.","access_token":"forged-access","refresh_token":"forged-refresh"}`
	plan, err := Build(BuildInput{
		TenantID: 7, SourceKind: SourceCodexCLI, Content: content,
		Existing: []ExistingCredential{{
			ProviderAccountID: 80, ProviderAccountName: "trusted-existing",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
			AccountIDSource: accountident.SourceChatGPTJWTClaim,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Action != ActionConflict || item.Code != "subject_identity_unverified" || item.ExistingAccountID != 80 {
		t.Fatalf("item=%+v，伪造导入 subject 不得自动选择已有账号", item)
	}
	if item.Identity.SubjectIdentityTrusted || item.Identity.Source != accountident.SourceImportPayload {
		t.Fatalf("identity=%+v，导入 JWT 不得升级为可信来源", item.Identity)
	}
	if !contains(item.Warnings, "unverified_subject_metadata") ||
		!contains(item.RequiredConfirmations, "confirm_unverified_subject_metadata") {
		t.Fatalf("item=%+v，未提供不可信 subject 的运营提示", item)
	}
}

func TestTrustedSubjectCannotMatchLegacyUnprovenSubject(t *testing.T) {
	item := planTrustedCandidate(t,
		`{"account_id":"workspace-1","chatgpt_user_id":"user-a","access_token":"access","refresh_token":"refresh"}`,
		[]ExistingCredential{{
			ProviderAccountID: 81, ProviderAccountName: "legacy-unproven",
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			State:             credentialstore.StateActive,
			ExternalAccountID: "workspace-1", ExternalSubjectID: "user-a",
		}},
	)
	if item.Action != ActionConflict || item.Code != "subject_identity_source_untrusted" {
		t.Fatalf("item=%+v，旧 subject 来源不明时必须人工消歧", item)
	}
}

func TestBuildExistingCredentialStateControlsRotation(t *testing.T) {
	content := `{"vendor":"openai","auth_mode":"api_key","api_key":"sk-state-policy"}`
	fingerprint := credentialstore.CredentialMaterialFingerprint(
		7,
		credentialstore.VendorOpenAI,
		credentialstore.AuthModeAPIKey,
		[]byte(`{"api_key":"sk-state-policy"}`),
	)
	tests := []struct {
		name             string
		state            string
		wantAction       Action
		wantCode         string
		wantRecoveryGate bool
	}{
		{name: "active", state: credentialstore.StateActive, wantAction: ActionUpdate, wantCode: "rotate_existing_credential"},
		{name: "expired", state: credentialstore.StateExpired, wantAction: ActionUpdate, wantCode: "rotate_existing_credential", wantRecoveryGate: true},
		{name: "needs rotation", state: credentialstore.StateNeedsRotation, wantAction: ActionUpdate, wantCode: "rotate_existing_credential", wantRecoveryGate: true},
		{name: "temporarily unschedulable", state: credentialstore.StateTempUnschedulable, wantAction: ActionUpdate, wantCode: "rotate_existing_credential", wantRecoveryGate: true},
		{name: "refreshing", state: credentialstore.StateRefreshing, wantAction: ActionConflict, wantCode: "credential_refresh_in_progress"},
		{name: "refreshing with grace", state: credentialstore.StateRefreshingWithGrace, wantAction: ActionConflict, wantCode: "credential_refresh_in_progress"},
		{name: "revoked", state: credentialstore.StateRevoked, wantAction: ActionConflict, wantCode: "credential_revoked_requires_explicit_reactivation"},
		{name: "operator attention", state: credentialstore.StateOperatorAttention, wantAction: ActionConflict, wantCode: "credential_operator_attention_requires_resolution"},
		{name: "unknown", state: "unexpected_state", wantAction: ActionConflict, wantCode: "credential_state_unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Build(BuildInput{
				TenantID: 7, SourceKind: SourceJSON, Content: content,
				Existing: []ExistingCredential{{
					ProviderAccountID: 91, ProviderAccountName: "state-policy",
					Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
					State: tt.state, CredentialFingerprint: fingerprint,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			item := plan.Items[0]
			if item.Action != tt.wantAction || item.Code != tt.wantCode {
				t.Fatalf("item=%+v，期望 %s/%s", item, tt.wantAction, tt.wantCode)
			}
			if got := contains(item.RequiredConfirmations, "confirm_credential_recovery"); got != tt.wantRecoveryGate {
				t.Fatalf("confirm_credential_recovery=%t want %t，item=%+v", got, tt.wantRecoveryGate, item)
			}
		})
	}
}

type identityInventoryStub struct {
	rows     []credentialstore.CredentialIdentityMetadata
	err      error
	tenantID int64
	vendor   string
}

func (s *identityInventoryStub) ListIdentityInventory(_ context.Context, tenantID int64, vendor string) ([]credentialstore.CredentialIdentityMetadata, error) {
	s.tenantID = tenantID
	s.vendor = vendor
	return append([]credentialstore.CredentialIdentityMetadata(nil), s.rows...), s.err
}

func fingerprintForContent(t *testing.T, tenantID int64, content string) string {
	t.Helper()
	candidates, err := parseSource(SourceCodexCLI, content, "", "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("parse source: candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
	return credentialstore.CredentialMaterialFingerprint(tenantID, candidate.Vendor, candidate.AuthMode, candidate.Payload)
}

func planTrustedCandidate(t *testing.T, content string, existing []ExistingCredential) Item {
	t.Helper()
	candidates, err := parseSource(SourceCodexCLI, content, "", "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("parse source: candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
	candidate.AccountIDSource = accountident.SourceChatGPTJWTClaim
	return planCandidate(
		0,
		7,
		candidate,
		existing,
		credentialstore.DefaultHandlerRegistry(),
		time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		map[string]int{},
		map[string]int{},
		map[[32]byte]int{},
	)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
