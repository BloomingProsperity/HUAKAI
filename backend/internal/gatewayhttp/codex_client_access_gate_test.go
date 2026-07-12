package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// TestEnforceOfficialClientAllowsOfficialCodexThroughGlobalLayer 端到端证明官方 Codex UA
// 经全局加固层(clientgate.Decide)放行(返回 nil failure,且提前 return、不触 reserveRes/结算)。
func TestEnforceOfficialClientAllowsOfficialCodexThroughGlobalLayer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("User-Agent", "codex_cli_rs/0.41.0")
	ex := &chatExecution{
		ctx:     context.Background(),
		r:       req,
		accInfo: provider.AccountInfo{Platform: "openai", AccountType: credentialstore.AuthModeCodexCLIOAuth, CodexCLIOnly: true},
	}
	if f := ex.enforceOfficialClient(); f != nil {
		t.Fatalf("官方 Codex UA 经全局加固层应放行,得 %+v", f)
	}
}
