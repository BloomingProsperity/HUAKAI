package embeddingshttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// ctxCaptureSettler 记录 Settle/Abort 收到的 ctx 取消态,用来证明结算调用
// 跑在脱离请求取消的 billingCtx 上(客户端断连不丢账)。
type ctxCaptureSettler struct {
	settleCtxErr error
	abortCtxErr  error
	settled      bool
	aborted      bool
}

func (s *ctxCaptureSettler) Settle(ctx context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	s.settled = true
	s.settleCtxErr = ctx.Err()
	return &billing.SettleResult{}, nil
}

func (s *ctxCaptureSettler) Abort(ctx context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	s.aborted = true
	s.abortCtxErr = ctx.Err()
	return nil
}

func (s *ctxCaptureSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *ctxCaptureSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

// 判别测试:请求 ctx 已取消(模拟客户端断连)时,abort 必须仍以未取消的
// billingCtx 调 Settler.Abort——否则额度 hold 永远卡死。
// Mutation guard: abort 改回 ex.ctx → abortCtxErr==context.Canceled → 红。
func TestEmbeddingsAbort_SurvivesClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stub := &ctxCaptureSettler{}
	ex := &execution{
		ctx:        ctx,
		d:          Deps{Settler: stub},
		ident:      auth.Identity{TenantID: 3},
		requestID:  "req-disconnect",
		reserveRes: &billing.ReserveResult{ClaimID: 7},
	}
	ex.abort(httptest.NewRecorder(), "test_reason", 0)
	if !stub.aborted {
		t.Fatal("abort 未调用 Settler.Abort")
	}
	if stub.abortCtxErr != nil {
		t.Fatalf("abort 必须跑在脱离请求取消的结算 ctx 上;got %v", stub.abortCtxErr)
	}
}

// billingCtx 语义:摘掉请求取消信号 + 带 5s 上界。
func TestEmbeddingsBillingCtx_DetachedWithDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ex := &execution{ctx: ctx}
	bctx, bcancel := ex.billingCtx()
	defer bcancel()
	if bctx.Err() != nil {
		t.Fatalf("billingCtx 必须脱离请求取消: %v", bctx.Err())
	}
	if _, ok := bctx.Deadline(); !ok {
		t.Fatal("billingCtx 必须带超时上界防挂起")
	}
}
