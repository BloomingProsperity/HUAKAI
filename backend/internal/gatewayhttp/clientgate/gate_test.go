package clientgate

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/codexclientaccess"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func reqWithUA(ua string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("User-Agent", ua)
	return r
}

func strictAnthropicGateFixture() (*http.Request, []byte) {
	body := []byte(`{"model":"claude-sonnet","max_tokens":8,"system":[{"type":"text","text":"supported CLI"}],"metadata":{"user_id":"user_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_account_00000000-1111-2222-3333-444444444444_session_11111111-2222-3333-4444-555555555555"},"messages":[{"role":"user","content":"hi"}]}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "claude-cli/2.1.78 (external, cli)")
	r.Header.Set("X-App", "cli")
	r.Header.Set("X-Stainless-Lang", "js")
	r.Header.Set("X-Stainless-Runtime", "node")
	r.Header.Set("X-Stainless-Package-Version", "0.74.0")
	r.Header.Set("X-Stainless-Retry-Count", "0")
	r.Header.Set("Anthropic-Version", "2023-06-01")
	r.Header.Set("Anthropic-Beta", "claude-code-20250219")
	return r, body
}

// TestDecideWithBodyAnthropicOfficialDirect 真 Claude Code 形态 → OfficialDirect。
func TestDecideWithBodyAnthropicOfficialDirect(t *testing.T) {
	r, body := strictAnthropicGateFixture()
	got := DecideWithBody(context.Background(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, r, body)
	if got.Decision != DecisionOfficialDirect || !bytes.Equal(got.Body, body) {
		t.Fatalf("官方 fixture result=%+v want official_direct + byte equivalent", got)
	}
}

// TestDecideWithBodyAnthropicThirdPartyBodyCloak 默认 body 伪装开 → 非官方 Allow(出站伪装)。
// 显式 HUAKAI_CLAUDE_OAUTH_BODY_CLOAK=false → 回退 Reject 严格门。
func TestDecideWithBodyAnthropicThirdPartyBodyCloak(t *testing.T) {
	r, body := strictAnthropicGateFixture()
	spoof := r.Clone(r.Context())
	spoof.Header = make(http.Header)
	spoof.Header.Set("User-Agent", "claude-cli/2.1.78 (external, cli)")
	spoof.Header.Set("X-Client-Name", "Claude Code")

	t.Setenv("HUAKAI_CLAUDE_OAUTH_BODY_CLOAK", "")
	got := DecideWithBody(context.Background(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, spoof, body)
	if got.Decision != DecisionAllow || got.Reason != "claude_oauth_body_cloak" {
		t.Fatalf("body cloak on: result=%+v want allow+claude_oauth_body_cloak", got)
	}

	t.Setenv("HUAKAI_CLAUDE_OAUTH_BODY_CLOAK", "false")
	got = DecideWithBody(context.Background(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, spoof, body)
	if got.Decision != DecisionReject || got.Reason != ReasonOfficialClientRequired {
		t.Fatalf("body cloak off: result=%+v want reject", got)
	}
}

// TestCodexAccessApplies 守住加固层生效范围:仅 codex vendor 反转号 + CodexCLIOnly 开启才生效;
// anthropic(走片2e 强制门)、codex 未开 knob、非反转号一律不接管。
func TestCodexAccessApplies(t *testing.T) {
	tests := []struct {
		name        string
		accountType string
		platform    string
		codexOnly   bool
		want        bool
	}{
		{"codex 反转号 knob 开", credentialstore.AuthModeCodexCLIOAuth, "openai", true, true},
		{"anthropic 反转号不接管", credentialstore.AuthModeCodexCLIOAuth, "anthropic", true, false},
		{"codex knob 关不接管", credentialstore.AuthModeCodexCLIOAuth, "openai", false, false},
		{"codex 非反转号不接管", "apikey", "openai", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodexAccessApplies(tt.accountType, tt.platform, tt.codexOnly); got != tt.want {
				t.Fatalf("CodexAccessApplies(%q,%q,%v) = %v, want %v", tt.accountType, tt.platform, tt.codexOnly, got, tt.want)
			}
		})
	}
}

// TestLoadCodexPolicyNilGetterDefaults 守住关键不变量:getter 为 nil(设置未接线)时默认策略
// allow_app_server=false、force 关、名单空——语义同片2e(仅官方客户端放行)。变异(默认误取 true)
// 会让下方 Evaluate 非官方拒用例红。
func TestLoadCodexPolicyNilGetterDefaults(t *testing.T) {
	policy := LoadCodexPolicy(context.TODO(), nil)
	if policy.AllowAppServer || policy.ForceAllow {
		t.Fatalf("nil getter 默认策略应全关,得 %+v", policy)
	}
	if len(policy.Blacklist) != 0 || len(policy.Whitelist) != 0 {
		t.Fatal("nil getter 默认名单应为空")
	}

	official := codexclientaccess.Evaluate(policy, codexclientaccess.Candidate{UserAgent: "codex_cli_rs/0.41.0"})
	if !official.Allow || official.Reason != codexclientaccess.ReasonMatchedOfficialUA {
		t.Fatalf("官方 Codex UA 默认应放行,得 %+v", official)
	}
	unknown := codexclientaccess.Evaluate(policy, codexclientaccess.Candidate{UserAgent: "curl/8.0"})
	if unknown.Allow || unknown.Reason != codexclientaccess.ReasonNotMatched {
		t.Fatalf("非官方客户端默认应拒绝,得 %+v", unknown)
	}
}

// TestDecideCodexPathAndFallback 证明 Decide 在 codex 加固层生效时按 Evaluate 判决(官方放/非官方拒),
// 且非 codex 情形回退官方客户端门(anthropic 反转号 + 非官方 UA → 拒)。getter 传 nil 走默认策略。
func TestDecideCodexPathAndFallback(t *testing.T) {
	if deny, _ := Decide(context.TODO(), nil, credentialstore.AuthModeCodexCLIOAuth, "openai", true, reqWithUA("codex_cli_rs/0.41.0")); deny {
		t.Fatal("codex 官方 UA 应放行")
	}

	deny, reason := Decide(context.TODO(), nil, credentialstore.AuthModeCodexCLIOAuth, "openai", true, reqWithUA("curl/8.0"))
	if !deny || reason != "codex_client_access:"+codexclientaccess.ReasonNotMatched {
		t.Fatalf("codex 非官方应拒且原因带前缀,得 deny=%v reason=%q", deny, reason)
	}

	deny, reason = Decide(context.TODO(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, reqWithUA("curl/8.0"))
	if !deny || reason != ReasonOfficialClientRequired {
		t.Fatalf("anthropic 非官方应走官方门拒,得 deny=%v reason=%q", deny, reason)
	}
}
