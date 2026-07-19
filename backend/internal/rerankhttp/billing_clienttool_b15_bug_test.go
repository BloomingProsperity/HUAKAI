package rerankhttp

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// TestB15_RerankSettleRequestCarriesClientTool 判别测试(bug B15 [S3]):
// rerank 的 settleRequest 必须把中间件归一出的 client tool 枚举写入
// usage_records(Draft.ClientTool),与 embeddingshttp/billing.go:140 同构,
// 保持三端(completions/embeddings/rerank)归属口径一致。
//
// 缺陷现状:rerankhttp/billing.go 的 settleRequest 不设 Draft.ClientTool
// → usage_records.client_tool 恒 NULL → 本断言红。
// 修复后:Draft.ClientTool = clientid.ToolFromContext(ex.ctx) → 绿。
func TestB15_RerankSettleRequestCarriesClientTool(t *testing.T) {
	ctx := clientid.WithIdentity(context.Background(), clientid.IdentityCursor, 1.0)
	ex := &execution{
		ctx:        ctx,
		ident:      auth.Identity{TenantID: 1},
		requestID:  "req-b15-rerank",
		reserveRes: &billing.ReserveResult{ClaimID: 1},
		selRes:     &pool.SelectionResult{},
	}
	req := ex.settleRequest("snap", 1)
	if got, want := req.Draft.ClientTool, string(clientid.IdentityCursor); got != want {
		t.Fatalf("rerank settle Draft.ClientTool=%q want %q —— client_tool 归属缺列(观测/归因面缺失)", got, want)
	}
}
