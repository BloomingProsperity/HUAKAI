package intake

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// AI-01:OAuth-only 模式的来源回校。通用 JSON/CSV/CLI 导入不能自声明 OAuth-only 的
// auth_mode(如 claude_ai_oauth)把手写 token 标成"官方 OAuth 获取"。
// 变异刀:删掉 planCandidate 里 SourceAllowedForMode 回校块 →
// claude_ai_oauth 那条不再拿 source_not_allowed_for_mode → 转红。
func TestPlanCandidate_RejectsOAuthOnlyModeViaImport(t *testing.T) {
	oauthOnly := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Payload: []byte(`{"access_token":"hand-written","refresh_token":"hand-written"}`),
	}
	for _, src := range []SourceKind{SourceJSON, SourceCSV, SourceCLI} {
		plan := BuildCandidates(BuildInput{TenantID: 7, SourceKind: src}, []credentialacq.CredentialCandidate{oauthOnly}).Plan
		if len(plan.Items) != 1 || plan.Items[0].Code != "source_not_allowed_for_mode" {
			t.Fatalf("claude_ai_oauth via %s 应被 source_not_allowed_for_mode 拒;got %+v", src, plan.Items)
		}
	}

	// 对照:claude_code(AllowedHelpers 含 json_import/cli_import)是粘贴既有 token 的正路,
	// 同样 payload 经 JSON 导入不得因"来源"被拒。
	pasteOK := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeCode,
		Payload: []byte(`{"access_token":"pasted","refresh_token":"pasted"}`),
	}
	okPlan := BuildCandidates(BuildInput{TenantID: 7, SourceKind: SourceJSON}, []credentialacq.CredentialCandidate{pasteOK}).Plan
	if len(okPlan.Items) != 1 || okPlan.Items[0].Code == "source_not_allowed_for_mode" {
		t.Fatalf("claude_code via JSON import 不应因来源被拒;got %+v", okPlan.Items)
	}
}
