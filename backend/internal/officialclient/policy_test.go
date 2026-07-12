package officialclient

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// claudeCodeReq 组装一个 Claude Code 兼容请求;caller 可先改 body/头再传入判定。
// 默认是带 system/metadata/beta 的主请求形态。
func claudeCodeReq(path, body string, headerOverride map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	base := map[string]string{
		"Content-Type":                "application/json; charset=utf-8",
		"User-Agent":                  "claude-cli/2.1.199 (external, cli)",
		"X-App":                       "cli",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Package-Version": "0.74.0",
		"X-Stainless-Retry-Count":     "0",
		"Anthropic-Version":           "2023-06-01",
		"Anthropic-Beta":              "claude-code-20250219",
	}
	for k, v := range base {
		r.Header.Set(k, v)
	}
	for k, v := range headerOverride {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	return r
}

const claudeCodeMainBody = `{"model":"claude-sonnet","max_tokens":8,"system":[{"type":"text","text":"CLI"}],"metadata":{"user_id":"u"},"messages":[{"role":"user","content":"hi"}]}`

// TestDecideAnthropicOfficialDirectByteEquivalent 证明兼容形态产出 OfficialDirect,
// 返回值与输入逐字节相等且不共享底层切片(alias==上游 model 时才字节等价,此处由
// 下游 model 改写接缝保证;门本身只克隆不改写)。
func TestDecideAnthropicOfficialDirectByteEquivalent(t *testing.T) {
	body := []byte(claudeCodeMainBody)
	before := bytes.Clone(body)
	got := DecideAnthropicOfficialDirect(claudeCodeReq("/v1/messages", claudeCodeMainBody, nil), body)
	if got.Decision != DirectDecisionOfficialDirect || got.Reason != ReasonShapeCompatible {
		t.Fatalf("decision=%q reason=%q want official_direct/shape_compatible", got.Decision, got.Reason)
	}
	if !bytes.Equal(got.Body, before) {
		t.Fatalf("直发 body 漂移\ngot:  %s\nwant: %s", got.Body, before)
	}
	got.Body[0] = '['
	if !bytes.Equal(body, before) {
		t.Fatal("返回 body 与输入共享底层切片")
	}
}

// TestDecideAnthropicOfficialDirectAcceptsRealClaudeShapes 咬住 S1-1/S1-2:真实 2.x 的
// 多入口(cli-bg/IDE/SDK)与辅助请求(缺 system/metadata/beta、max_tokens=0/1、计数
// fallback、旧裸 UA)都必须放行,而非因"没带全字段/入口非 cli"被误拒 403。
// 变异:把 X-App 判据改回 =="cli" / body 判据改回要求 system 非空或 max_tokens>0 →
// 对应用例变红。
func TestDecideAnthropicOfficialDirectAcceptsRealClaudeShapes(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{"后台 cli-bg", "/v1/messages", claudeCodeMainBody, map[string]string{"X-App": "cli-bg"}},
		{"VSCode 入口", "/v1/messages", claudeCodeMainBody, map[string]string{"User-Agent": "claude-cli/2.1.199 (external, claude-vscode)"}},
		{"TS Agent SDK 入口", "/v1/messages", claudeCodeMainBody, map[string]string{"User-Agent": "claude-cli/2.1.199 (external, sdk-ts)"}},
		{"未知合法 IDE 入口", "/v1/messages", claudeCodeMainBody, map[string]string{"User-Agent": "claude-cli/2.1.199 (external, jetbrains)"}},
		{"旧裸 UA 无 external 后缀", "/v1/messages", claudeCodeMainBody, map[string]string{"User-Agent": "claude-cli/2.1.63"}},
		{"缺 system", "/v1/messages", `{"model":"claude-sonnet","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, nil},
		{"缺 metadata", "/v1/messages", `{"model":"claude-sonnet","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, nil},
		{"缺 beta 头", "/v1/messages", claudeCodeMainBody, map[string]string{"Anthropic-Beta": ""}},
		{"max_tokens=0 填充缓存", "/v1/messages", `{"model":"claude-sonnet","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, nil},
		{"计数 fallback max_tokens=1 无 system", "/v1/messages", `{"model":"claude-haiku","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideAnthropicOfficialDirect(claudeCodeReq(tc.path, tc.body, tc.headers), []byte(tc.body))
			if got.Decision != DirectDecisionOfficialDirect {
				t.Fatalf("兼容形态被误拒: decision=%q reason=%q", got.Decision, got.Reason)
			}
		})
	}
}

// TestDecideAnthropicOfficialDirectRejectsNonClaudeShapes 证明非 Claude 形态/协议核心
// 缺失/自报头冲突仍被拒。注意:形态门只挡"不像 Claude 请求",不做访问控制——完整
// 伪造头但无 API key 的拦截由上游认证层负责,不在此门。
func TestDecideAnthropicOfficialDirectRejectsNonClaudeShapes(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{"UA 只作 substring", "/v1/messages", claudeCodeMainBody, map[string]string{"User-Agent": "curl/8.0 claude-cli/2.1.199 (external, cli)"}},
		{"缺 X-App", "/v1/messages", claudeCodeMainBody, map[string]string{"X-App": ""}},
		{"X-App 非集合值", "/v1/messages", claudeCodeMainBody, map[string]string{"X-App": "web"}},
		{"缺 Stainless lang", "/v1/messages", claudeCodeMainBody, map[string]string{"X-Stainless-Lang": ""}},
		{"package 非 semver", "/v1/messages", claudeCodeMainBody, map[string]string{"X-Stainless-Package-Version": "latest"}},
		{"anthropic-version 不受支持", "/v1/messages", claudeCodeMainBody, map[string]string{"Anthropic-Version": "1999-12-31"}},
		{"retry 负数", "/v1/messages", claudeCodeMainBody, map[string]string{"X-Stainless-Retry-Count": "-1"}},
		{"错误入口路径", "/v1/chat/completions", claudeCodeMainBody, nil},
		{"缺 model", "/v1/messages", `{"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, nil},
		{"空 messages", "/v1/messages", `{"model":"claude-sonnet","max_tokens":8,"messages":[]}`, nil},
		{"/v1/messages 缺 max_tokens", "/v1/messages", `{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`, nil},
		{"max_tokens 负数", "/v1/messages", `{"model":"claude-sonnet","max_tokens":-1,"messages":[{"role":"user","content":"hi"}]}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideAnthropicOfficialDirect(claudeCodeReq(tc.path, tc.body, tc.headers), []byte(tc.body))
			if got.Decision != DirectDecisionReject || len(got.Body) != 0 {
				t.Fatalf("非 Claude 形态未拒: decision=%q body=%q", got.Decision, got.Body)
			}
		})
	}
}

// TestClaudeCodeXAppRejectsConflictingDuplicates 咬住冲突重复 X-App 头被拒。
func TestClaudeCodeXAppRejectsConflictingDuplicates(t *testing.T) {
	r := claudeCodeReq("/v1/messages", claudeCodeMainBody, nil)
	r.Header.Set("X-App", "cli")
	r.Header.Add("X-App", "cli-bg") // 冲突重复
	if got := DecideAnthropicOfficialDirect(r, []byte(claudeCodeMainBody)); got.Decision != DirectDecisionReject {
		t.Fatalf("冲突重复 X-App 应拒,得 decision=%q", got.Decision)
	}
}

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
