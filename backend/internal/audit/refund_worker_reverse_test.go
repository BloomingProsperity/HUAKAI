package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

type reverseCall struct {
	tenantID int64
	claimID  int64
	micros   int64
}

type recordingQuotaReverser struct {
	calls []reverseCall
	err   error
	skip  bool
}

func (r *recordingQuotaReverser) ReverseSettledCost(_ context.Context, tenantID, claimID int64, amountMicroUSD int64) (QuotaReverseResult, error) {
	r.calls = append(r.calls, reverseCall{tenantID: tenantID, claimID: claimID, micros: amountMicroUSD})
	return QuotaReverseResult{Skipped: r.skip}, r.err
}

// TestMismatchRefundWorker_ReversesQuotaWithActualRefund 钉死 ③ 接线:退款落库后,worker 用退款的
// 实际金额(RefundMicroUSD)调用配额冲减器,且租户/claim/金额一致。
// 判别(§14):若删掉 applyLegacy/applyInTx 里的 w.reverseQuotaAfterRefund 调用,len(calls)=0,转红。
func TestMismatchRefundWorker_ReversesQuotaWithActualRefund(t *testing.T) {
	worker, settler, _, _, payload := refundWorkerFixture(t, nil)
	rev := &recordingQuotaReverser{}
	worker.quotaReverser = rev // 同包测试:直接注入,等价 WithRefundQuotaReverser

	if err := worker.Apply(context.Background(), payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refundCalls=%d want 1", settler.refundCalls)
	}
	if len(rev.calls) != 1 {
		t.Fatalf("配额冲减调用次数=%d want 1", len(rev.calls))
	}
	if c := rev.calls[0]; c.tenantID != 9 || c.claimID != 1001 || c.micros != 40 {
		t.Fatalf("冲减入参=%+v want tenant=9 claim=1001 micros=40(=退款实际金额)", c)
	}
}

// TestReverseQuotaAfterRefund_GuardsZeroNilIdempotentAndAbsent 钉死守卫:nil 退款 / 零退款 /
// 幂等重放 / 未注入冲减器 → 不触发冲减;只有"本轮真发生的新退款"才按【实退额】冲减一次。
// 判别(§14):
//   - 若把 refund.RefundMicroUSD<=0 守卫放宽成 <0,零退款会误触发,转红;
//   - 若去掉 refund.Idempotent 守卫,幂等重放(正额 + Idempotent=true)会二次冲减,转红;
//   - 若把冲减额从 refund.RefundMicroUSD 改成 payload.DeltaMicroUSD,末例会冲 99 而非实退 25,转红。
func TestReverseQuotaAfterRefund_GuardsZeroNilIdempotentAndAbsent(t *testing.T) {
	rev := &recordingQuotaReverser{}
	w := &MismatchRefundWorker{quotaReverser: rev}
	// DeltaMicroUSD=99(请求额)故意区别于后面的实退额,用来判别"跟随实退、非请求 delta"。
	payload := MismatchRefundPayload{TenantID: 9, ClaimID: 1001, DeltaMicroUSD: 99}

	w.reverseQuotaAfterRefund(context.Background(), payload, nil)                                      // nil 退款
	w.reverseQuotaAfterRefund(context.Background(), payload, &billing.RefundResult{RefundMicroUSD: 0}) // 零退款
	// 幂等重放:billing 对同 audit_request_id 重放返回【存储的正退款额 + Idempotent=true】,本轮非新退款,不得再冲。
	w.reverseQuotaAfterRefund(context.Background(), payload, &billing.RefundResult{RefundMicroUSD: 40, Idempotent: true})
	if len(rev.calls) != 0 {
		t.Fatalf("nil/零/幂等重放退款不应触发冲减,calls=%+v", rev.calls)
	}

	// 未注入冲减器:不 panic、不调用。
	(&MismatchRefundWorker{}).reverseQuotaAfterRefund(context.Background(), payload, &billing.RefundResult{RefundMicroUSD: 25})

	// 本轮新退款:按【实退额 25】冲减一次,而非请求的 DeltaMicroUSD=99。
	w.reverseQuotaAfterRefund(context.Background(), payload, &billing.RefundResult{RefundMicroUSD: 25})
	if len(rev.calls) != 1 || rev.calls[0].micros != 25 {
		t.Fatalf("本轮新退款应按实退 25 冲减一次(非请求 delta 99),calls=%+v", rev.calls)
	}
}

// TestReverseQuotaAfterRefund_RecordsSkippedResult 钉死 S3-4 一级:配额冲减器返回 Skipped
// 时必须有可观测信号,否则非 settled 预留退款会静默丢失冲减。判别(§14):删掉 Add(1) 或
// WARN 日志分支,计数 / 日志断言会转红。
func TestReverseQuotaAfterRefund_RecordsSkippedResult(t *testing.T) {
	refundQuotaReverseSkippedTotal.Set(0)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		refundQuotaReverseSkippedTotal.Set(0)
	})

	rev := &recordingQuotaReverser{skip: true}
	w := &MismatchRefundWorker{quotaReverser: rev}
	payload := MismatchRefundPayload{TenantID: 9, ClaimID: 1001, DeltaMicroUSD: 40}

	w.reverseQuotaAfterRefund(context.Background(), payload, &billing.RefundResult{RefundMicroUSD: 40})

	if got := refundQuotaReverseSkippedTotal.Value(); got != 1 {
		t.Fatalf("refund_quota_reverse_skipped_total=%d want 1", got)
	}
	if !strings.Contains(logs.String(), "配额 settled_value 冲减跳过") || !strings.Contains(logs.String(), "claim_id=1001") {
		t.Fatalf("skip WARN 日志缺失关键上下文: %s", logs.String())
	}
}
