package officialclient

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// TestRequiredIdentity_覆盖 验证 vendor→官方客户端身份映射(含大小写不敏感、未覆盖 vendor)。
func TestRequiredIdentity_覆盖(t *testing.T) {
	if id, ok := RequiredIdentity("anthropic"); !ok || id != clientid.IdentityClaudeCode {
		t.Fatalf("anthropic 应映射 Claude Code,得 id=%q ok=%v", id, ok)
	}
	if id, ok := RequiredIdentity("  Claude  "); !ok || id != clientid.IdentityClaudeCode {
		t.Fatalf("大小写/空白不敏感 Claude 应映射 Claude Code,得 id=%q ok=%v", id, ok)
	}
	if id, ok := RequiredIdentity("openai"); !ok || id != clientid.IdentityCodexCLI {
		t.Fatalf("openai 应映射 Codex CLI,得 id=%q ok=%v", id, ok)
	}
	if _, ok := RequiredIdentity("some_apikey_vendor"); ok {
		t.Fatalf("未覆盖 vendor 应 ok=false")
	}
}

// TestIsReverseAccountType 验证真实 AuthMode 值分类:OAuth/session 类=反转号,
// 官方 API key / 云凭据类 + 空/未知=否(大小写空白不敏感)。此分类还供出站身份改写使用。
func TestIsReverseAccountType(t *testing.T) {
	for _, at := range []string{
		credentialstore.AuthModeClaudeAIOAuth,
		credentialstore.AuthModeClaudeCode,
		credentialstore.AuthModeChatGPTOAuth,
		credentialstore.AuthModeCodexCLIOAuth,
		credentialstore.AuthModeCodexWebOAuth,
		credentialstore.AuthModeCodeAssist,
		credentialstore.AuthModeGoogleOne,
		"CLAUDE_AI_OAUTH",
		" codex_cli_oauth ",
	} {
		if !IsReverseAccountType(at) {
			t.Fatalf("%q 应为反转号", at)
		}
	}
	for _, at := range []string{
		credentialstore.AuthModeAPIKey,
		credentialstore.AuthModeAIStudioAPIKey,
		credentialstore.AuthModeBedrock,
		credentialstore.AuthModeVertexAnthropic,
		credentialstore.AuthModeAzure,
		"",
		"something_new",
	} {
		if IsReverseAccountType(at) {
			t.Fatalf("%q 不应为反转号", at)
		}
	}
}

// TestGateDecision 验证:官方 key 号恒不拒;Anthropic 反转号仍强制 Claude Code;
// OpenAI/codex/chatgpt 反转号默认放开;官方客户端身份仍放行(用真实 AuthMode 值)。
//
// 变异证伪:把 vendorEnforcesOfficialClient 的 default 改成 return true,
// codex/openai 非官方客户端 case 会被拒 → 测试红。
func TestGateDecision(t *testing.T) {
	tests := []struct {
		name        string
		accountType string
		vendor      string
		identity    clientid.Identity
		wantReject  bool
		wantReason  string
		checkReason bool
	}{
		{
			name:        "claude oauth 非官方客户端仍拒",
			accountType: credentialstore.AuthModeClaudeAIOAuth,
			vendor:      credentialstore.VendorAnthropic,
			identity:    clientid.IdentityCurlScript,
			wantReject:  true,
			wantReason:  ReasonNonOfficialReject,
			checkReason: true,
		},
		{
			name:        "claude code 账号非官方客户端仍拒",
			accountType: credentialstore.AuthModeClaudeCode,
			vendor:      "claude",
			identity:    clientid.IdentityChatUI,
			wantReject:  true,
			wantReason:  ReasonNonOfficialReject,
			checkReason: true,
		},
		{
			name:        "claude oauth 官方客户端放行",
			accountType: credentialstore.AuthModeClaudeAIOAuth,
			vendor:      credentialstore.VendorAnthropic,
			identity:    clientid.IdentityClaudeCode,
			wantReject:  false,
			wantReason:  ReasonOfficialClientOK,
			checkReason: true,
		},
		{
			name:        "codex cli oauth 非官方客户端默认放开",
			accountType: credentialstore.AuthModeCodexCLIOAuth,
			vendor:      credentialstore.VendorOpenAI,
			identity:    clientid.IdentityClaudeCode,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
			checkReason: true,
		},
		{
			name:        "codex web oauth 非官方客户端默认放开",
			accountType: credentialstore.AuthModeCodexWebOAuth,
			vendor:      "codex",
			identity:    clientid.IdentityCurlScript,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
			checkReason: true,
		},
		{
			name:        "chatgpt oauth 非官方客户端默认放开",
			accountType: credentialstore.AuthModeChatGPTOAuth,
			vendor:      "chatgpt",
			identity:    clientid.IdentityUnknown,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
			checkReason: true,
		},
		{
			name:        "codex cli oauth 官方客户端放行",
			accountType: credentialstore.AuthModeCodexCLIOAuth,
			vendor:      credentialstore.VendorOpenAI,
			identity:    clientid.IdentityCodexCLI,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
			checkReason: true,
		},
		{
			name:        "api key 账号 anthropic 非官方客户端不拒",
			accountType: credentialstore.AuthModeAPIKey,
			vendor:      credentialstore.VendorAnthropic,
			identity:    clientid.IdentityCurlScript,
			wantReject:  false,
			wantReason:  ReasonNoRestriction,
			checkReason: true,
		},
		{
			name:        "api key 账号 openai 官方客户端不拒",
			accountType: credentialstore.AuthModeAPIKey,
			vendor:      credentialstore.VendorOpenAI,
			identity:    clientid.IdentityCodexCLI,
			wantReject:  false,
			wantReason:  ReasonNoRestriction,
			checkReason: true,
		},
		{
			name:        "非强制 vendor 的反转号不拒",
			accountType: credentialstore.AuthModeCodeAssist,
			vendor:      "gemini",
			identity:    clientid.IdentityCurlScript,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
			checkReason: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reject, reason := GateDecision(tt.accountType, tt.vendor, tt.identity, false)
			if reject != tt.wantReject {
				t.Fatalf("reject=%v, want %v, reason=%q", reject, tt.wantReject, reason)
			}
			if tt.checkReason && reason != tt.wantReason {
				t.Fatalf("reason=%q, want %q", reason, tt.wantReason)
			}
		})
	}

	for _, vendor := range []string{credentialstore.VendorAnthropic, credentialstore.VendorOpenAI, "codex", "chatgpt", "gemini"} {
		if reject, reason := GateDecision(credentialstore.AuthModeAPIKey, vendor, clientid.IdentityCurlScript, false); reject || reason != ReasonNoRestriction {
			t.Fatalf("api_key 号不应拒 vendor=%q,得 reject=%v reason=%q", vendor, reject, reason)
		}
	}
}

// TestGateDecisionForceOfficialClient 验证账号级 forceOfficialClient opt-in:codex 默认放开、
// 开关打开后非官方客户端被拒、官方 Codex CLI 放行,且不越过反转账号前置门、不削弱 Anthropic。
func TestGateDecisionForceOfficialClient(t *testing.T) {
	tests := []struct {
		name                string
		accountType         string
		vendor              string
		identity            clientid.Identity
		forceOfficialClient bool
		wantReject          bool
		wantReason          string
	}{
		{
			name:        "anthropic 反转账号非 Claude Code 拒绝",
			accountType: credentialstore.AuthModeClaudeAIOAuth,
			vendor:      credentialstore.VendorAnthropic,
			identity:    clientid.IdentityCurlScript,
			wantReject:  true,
			wantReason:  ReasonNonOfficialReject,
		},
		{
			name:        "anthropic 反转账号 Claude Code 放行",
			accountType: credentialstore.AuthModeClaudeAIOAuth,
			vendor:      credentialstore.VendorAnthropic,
			identity:    clientid.IdentityClaudeCode,
			wantReject:  false,
			wantReason:  ReasonOfficialClientOK,
		},
		{
			name:        "codex 反转账号默认不强制非 CLI 放行",
			accountType: credentialstore.AuthModeCodexCLIOAuth,
			vendor:      credentialstore.VendorOpenAI,
			identity:    clientid.IdentityCurlScript,
			wantReject:  false,
			wantReason:  ReasonVendorNotEnforced,
		},
		{
			name:                "codex 反转账号强制后非 CLI 拒绝",
			accountType:         credentialstore.AuthModeCodexCLIOAuth,
			vendor:              credentialstore.VendorOpenAI,
			identity:            clientid.IdentityCurlScript,
			forceOfficialClient: true,
			wantReject:          true,
			wantReason:          ReasonNonOfficialReject,
		},
		{
			name:                "codex 反转账号强制后 CLI 放行",
			accountType:         credentialstore.AuthModeCodexCLIOAuth,
			vendor:              credentialstore.VendorOpenAI,
			identity:            clientid.IdentityCodexCLI,
			forceOfficialClient: true,
			wantReject:          false,
			wantReason:          ReasonOfficialClientOK,
		},
		{
			name:                "apikey 类账号即使 force 也不越过反转前置门",
			accountType:         credentialstore.AuthModeAPIKey,
			vendor:              credentialstore.VendorOpenAI,
			identity:            clientid.IdentityCurlScript,
			forceOfficialClient: true,
			wantReject:          false,
			wantReason:          ReasonNoRestriction,
		},
		{
			name:                "无官方客户端映射 vendor 即使 force 也 fail-open 不误杀",
			accountType:         credentialstore.AuthModeCodeAssist,
			vendor:              "gemini",
			identity:            clientid.IdentityCurlScript,
			forceOfficialClient: true,
			wantReject:          false,
			wantReason:          ReasonVendorNoOfficial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReject, gotReason := GateDecision(tt.accountType, tt.vendor, tt.identity, tt.forceOfficialClient)
			if gotReject != tt.wantReject {
				t.Fatalf("reject = %v, want %v, reason = %s", gotReject, tt.wantReject, gotReason)
			}
			if gotReason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}
