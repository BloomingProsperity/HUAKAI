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

// TestEnforceOfficialClient_CodexCLIOnlyGatesNonOfficialClient 守住片2e 接线主线:
// enforceOfficialClient 必须把 AccountInfo.CodexCLIOnly 真正接进门控——knob 开 + 非官方
// 客户端拒(403 非 nil failure)、官方 Codex CLI 放行、knob 关默认放行。变异(第 586 行
// 传 false、或字段不流入)会让"knob 开 + 非 CLI"用例返回 nil→本测试红。
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
	if f := newExec(true, "codex-cli/1.2.3").enforceOfficialClient(); f != nil {
		t.Fatalf("codex 账号 knob 开 + Codex CLI 应放行,得 %+v", f)
	}
	if f := newExec(false, "curl/8.0").enforceOfficialClient(); f != nil {
		t.Fatalf("codex 账号 knob 关默认放行,得 %+v", f)
	}
}
