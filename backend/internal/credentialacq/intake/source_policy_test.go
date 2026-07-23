package intake

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestSourceAllowedForMode使用专用来源矩阵(t *testing.T) {
	tests := []struct {
		name     string
		source   SourceKind
		vendor   string
		authMode string
		want     bool
	}{
		{"Cookie OAuth", SourceClaudeCookie, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, true},
		{"Cookie 不得进入 Setup", SourceClaudeCookie, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, false},
		{"Cookie 不得跨厂商", SourceClaudeCookie, credentialstore.VendorOpenAI, credentialstore.AuthModeClaudeAIOAuth, false},
		{"Setup Cookie", SourceClaudeSetupCookie, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, true},
		{"Setup Cookie 不得进入 OAuth", SourceClaudeSetupCookie, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, false},
		{"Setup Cookie 不得跨厂商", SourceClaudeSetupCookie, credentialstore.VendorOpenAI, credentialstore.AuthModeClaudeSetupToken, false},
		{"Codex Agent", SourceCodexAgent, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent, true},
		{"Codex Agent 不得跨厂商", SourceCodexAgent, credentialstore.VendorAnthropic, credentialstore.AuthModeCodexAgent, false},
		{"Codex Agent 不得进入普通 OAuth", SourceCodexAgent, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, false},
		{"CRS Claude OAuth", SourceCRSSync, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, true},
		{"CRS OpenAI OAuth", SourceCRSSync, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, true},
		{"CRS Gemini Key", SourceCRSSync, credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, true},
		{"CRS 不得跨厂商模式", SourceCRSSync, credentialstore.VendorAnthropic, credentialstore.AuthModeChatGPTOAuth, false},
		{"CRS 不得进入 Codex Web", SourceCRSSync, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth, false},
		{"迁移包允许恢复已发布模式", SourceAccountBundle, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, true},
		{"未知来源关闭", SourceKind("unknown"), credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SourceAllowedForMode(test.source, test.vendor, test.authMode); got != test.want {
				t.Fatalf("SourceAllowedForMode(%s,%s,%s)=%v，期望 %v",
					test.source, test.vendor, test.authMode, got, test.want)
			}
		})
	}
}

func Test专用来源影响统一计划且不绕过发布闸(t *testing.T) {
	claudeOAuth := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Payload: []byte(`{"access_token":"access","refresh_token":"refresh"}`),
	}
	assertCode := func(t *testing.T, source SourceKind, candidate credentialacq.CredentialCandidate, want string) {
		t.Helper()
		plan := BuildCandidates(BuildInput{TenantID: 7, SourceKind: source}, []credentialacq.CredentialCandidate{candidate}).Plan
		if len(plan.Items) != 1 || plan.Items[0].Code != want {
			t.Fatalf("source=%s item=%+v，期望 code=%s", source, plan.Items, want)
		}
	}

	setupToken := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeSetupToken,
		Payload: []byte(`{"setup_token":"setup-token"}`),
	}
	for _, source := range []SourceKind{SourceCLI, SourceJSON, SourceCSV} {
		assertCode(t, source, claudeOAuth, "source_not_allowed_for_mode")
		assertCode(t, source, setupToken, "source_not_allowed_for_mode")
	}
	assertCode(t, SourceClaudeCookie, claudeOAuth, "create_account")
	assertCode(t, SourceAccountBundle, claudeOAuth, "create_account")

	sealed := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth,
		Payload: []byte(`{"access_token":"sealed"}`),
	}
	assertCode(t, SourceAccountBundle, sealed, "credential_mode_sealed")
}
