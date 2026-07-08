package clientgate

import (
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
