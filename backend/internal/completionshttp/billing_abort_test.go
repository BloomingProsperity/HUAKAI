package completionshttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// completionsAbortCtxSpy 捕获 Abort 收到的 ctx 状态。
type completionsAbortCtxSpy struct {
	calls      int
	lastCtxErr error
}

func (s *completionsAbortCtxSpy) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *completionsAbortCtxSpy) Abort(ctx context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	s.calls++
	s.lastCtxErr = ctx.Err()
	return nil
}

func (s *completionsAbortCtxSpy) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *completionsAbortCtxSpy) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

// TestCompletionsAbortUsesDetachedCtx 守 C-1(completions 半边):客户端断连(ex.ctx 取消)时,
// abort 仍以脱离 ctx 释放 hold 与并发槽,不泄漏到 lease 过期。与 gatewayhttp、以及
// images/rerank/embeddings/audio(billingCtx)同构——completions 曾是唯一直用 ex.ctx 的兄弟。
//
// Mutation:billing.go 的 abort 改回 Abort(ex.ctx, ...) → Abort 收到已取消 ctx →
// lastCtxErr != nil → 红。
func TestCompletionsAbortUsesDetachedCtx(t *testing.T) {
	spy := &completionsAbortCtxSpy{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel() // 模拟客户端断连:ex.ctx 被取消
	ex := &execution{
		d:          Deps{Settler: spy},
		ctx:        canceled,
		ident:      auth.Identity{TenantID: 100},
		requestID:  "req-completions-abort",
		reserveRes: &billing.ReserveResult{ClaimID: 7},
	}
	ex.abort(httptest.NewRecorder(), "test_reason", 0)
	if spy.calls != 1 {
		t.Fatalf("abort 应触发 1 次 Settler.Abort,实际 %d", spy.calls)
	}
	if spy.lastCtxErr != nil {
		t.Fatalf("abort 在已取消 ctx 上执行(err=%v)—— 未用脱离 ctx(C-1 槽/hold 泄漏)", spy.lastCtxErr)
	}
}
