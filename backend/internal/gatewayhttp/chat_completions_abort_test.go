package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// abortCtxSpySettler 捕获 Abort 收到的 ctx 状态。
type abortCtxSpySettler struct {
	calls      int
	lastCtxErr error
}

func (s *abortCtxSpySettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *abortCtxSpySettler) Abort(ctx context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	s.calls++
	s.lastCtxErr = ctx.Err()
	return nil
}

func (s *abortCtxSpySettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
	return nil
}

func (s *abortCtxSpySettler) Refund(_ context.Context, _ billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

// TestAbortReservation_UsesDetachedCtx 守 C-1:客户端断连(ex.ctx 取消)时,abort 仍以未取消的
// 脱离 ctx 执行,hold/并发槽得以释放,不泄漏到 lease 过期。
//
// Mutation:abortReservation 改回 ex.d.Settler.Abort(ex.ctx, ...) → Abort 收到已取消 ctx →
// lastCtxErr != nil → 红。
func TestAbortReservation_UsesDetachedCtx(t *testing.T) {
	spy := &abortCtxSpySettler{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel() // 模拟客户端断连:ex.ctx 被取消
	ex := &chatExecution{
		ctx:       canceled,
		d:         ChatHandlerDeps{Settler: spy},
		ident:     auth.Identity{TenantID: 100},
		requestID: "req-c1",
	}
	if err := ex.abortReservation(7, "test_reason", 0, nil); err != nil {
		t.Fatalf("脱离 ctx 上 abort 不应返回 error: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("Abort calls=%d want 1", spy.calls)
	}
	if spy.lastCtxErr != nil {
		t.Fatalf("Abort 在已取消 ctx 上执行(err=%v)—— 未用脱离 ctx(C-1 槽/hold 泄漏)", spy.lastCtxErr)
	}
}

// TestServeL2CacheHit_AbortUsesDetachedCtx 守 C-1 的【L2 缓存命中半边】:命中后审计 ledger
// 落库失败(production + AuditLedger 未配)时,serveL2CacheHit 要 abort 释放已持有的 hold
// 与并发槽(AccountID!=0 表示 acquire 已完成)。客户端断连(ctx 取消)时该释放必须走脱离
// ctx——否则 abort 失败,hold+并发槽泄漏到 lease 过期。这是与 ex.abortReservation 配对的
// 另一条请求路径,单测 ex 方法测不到。
//
// Mutation:把 serveL2CacheHit 的 detachedAbort 改回 d.Settler.Abort(ctx, ...) → abort 收到
// 已取消 ctx → lastCtxErr != nil → 红。
func TestServeL2CacheHit_AbortUsesDetachedCtx(t *testing.T) {
	// production + AuditLedger==nil → submitAuditLedgerEntry 返错 → 触发 audit_ledger_error abort。
	t.Setenv("HUAKAI_RELEASE_MODE", "production")

	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:      "chatcmpl-cache-abort",
		Model:   "gpt-4o",
		Content: []proto.CanonicalContentBlock{{Type: "text", Text: "cached"}},
		Usage:   proto.CanonicalUsage{InputTokens: 1, OutputTokens: 1},
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	envelope, ok := encodeL2CacheEnvelope(env)
	if !ok {
		t.Fatal("encodeL2CacheEnvelope 失败")
	}

	spy := &abortCtxSpySettler{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel() // 模拟客户端断连:请求 ctx 被取消

	in := l2CacheHitInput{
		Entry:         l2cache.Entry{Envelope: envelope},
		Ident:         auth.Identity{TenantID: 100},
		RequestID:     "req-cache-abort",
		AccountID:     42, // !=0:acquire 已完成,hold+并发槽都已持有,abort 需释放两者
		ReserveResult: &billing.ReserveResult{ClaimID: 7},
	}
	d := ChatHandlerDeps{Settler: spy} // AuditLedger 未配

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	if served := serveL2CacheHit(canceled, rec, req, d, in); !served {
		t.Fatalf("serveL2CacheHit 应处理该命中(返回 true),实际 false;status=%d", rec.Code)
	}
	if spy.calls != 1 {
		t.Fatalf("audit_ledger_error 应触发 1 次 abort,实际 %d", spy.calls)
	}
	if spy.lastCtxErr != nil {
		t.Fatalf("cache-hit abort 在已取消 ctx 上执行(err=%v)—— serveL2CacheHit 未走 detachedAbort(hold+并发槽泄漏)", spy.lastCtxErr)
	}
}

// TestRejectMoneyPathAuditRef_AbortUsesDetachedCtx 守 C-1 的【audit-ref-missing 汇聚点】:
// cache-hit-commit 与 direct-settle 两条请求路径的零成本 abort 都汇到 rejectMoneyPathAuditRef,
// 断连时同样需脱离 ctx 释放,否则漏 hold/并发槽。
//
// Mutation:把 :396 的 detachedAbort 改回 d.Settler.Abort(ctx, ...) → lastCtxErr != nil → 红。
func TestRejectMoneyPathAuditRef_AbortUsesDetachedCtx(t *testing.T) {
	spy := &abortCtxSpySettler{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	event := eventbus.RequestCompletionEvent{TenantID: 100, ClaimID: 7, RequestID: "req-reject"}
	if _, abortErr := rejectMoneyPathAuditRef(canceled, ChatHandlerDeps{Settler: spy}, event, nil, "test_source"); abortErr != nil {
		t.Fatalf("脱离 ctx 上 abort 不应返回 error: %v", abortErr)
	}
	if spy.calls != 1 {
		t.Fatalf("audit-ref-missing 应触发 1 次 abort,实际 %d", spy.calls)
	}
	if spy.lastCtxErr != nil {
		t.Fatalf("audit-ref abort 在已取消 ctx 上执行(err=%v)—— :396 未走 detachedAbort", spy.lastCtxErr)
	}
}
