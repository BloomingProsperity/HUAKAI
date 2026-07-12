package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// TestEnforceOfficialClient_CodexCLIOnlyGatesNonOfficialClient 守住 CodexCLIOnly 端到端门控:
// knob 开 + 非官方客户端拒(403 非 nil failure)、官方 Codex CLI 放行、knob 关默认放行。
// 片2f 后 codex 反转号 + knob 开走 codexclientaccess.Evaluate 统一评估器(strict 官方匹配),
// 官方 UA 用真实 codex-rs 形态 codex_cli_rs/;knob 关仍走片2e 原 GateDecision 路径。变异
// (接线漏判 CodexCLIOnly、或 Evaluate 放行非官方)会让"knob 开 + 非官方"用例返回 nil→本测试红。
func TestEnforceOfficialClient_CodexCLIOnlyGatesNonOfficialClient(t *testing.T) {
	newExec := func(codexCLIOnly bool, userAgent string) *chatExecution {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.Header.Set("User-Agent", userAgent)
		return &chatExecution{
			ctx:       context.Background(),
			r:         r,
			requestID: "req-official-gate",
			accInfo: provider.AccountInfo{
				AccountType:  credentialstore.AuthModeCodexCLIOAuth,
				Platform:     "openai",
				CodexCLIOnly: codexCLIOnly,
			},
			reserveRes: &billing.ReserveResult{ClaimID: 771},
			d:          ChatHandlerDeps{Settler: &recordingSettler{}},
		}
	}

	if f := newExec(true, "curl/8.0").enforceOfficialClient(); f == nil {
		t.Fatal("codex 账号 knob 开 + 非 Codex CLI 应 403 拒绝(得 nil=放行)")
	}
	if f := newExec(true, "codex_cli_rs/0.41.0").enforceOfficialClient(); f != nil {
		t.Fatalf("codex 账号 knob 开 + 官方 Codex CLI 应放行,得 %+v", f)
	}
	if f := newExec(false, "curl/8.0").enforceOfficialClient(); f != nil {
		t.Fatalf("codex 账号 knob 关默认放行,得 %+v", f)
	}
}
