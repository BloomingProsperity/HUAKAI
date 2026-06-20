package billing

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestStreamStateTransitionMatrix(t *testing.T) {
	cases := []struct {
		from StreamState
		to   StreamState
		ok   bool
	}{
		{StreamStateAcquired, StreamStateAcquired, true},
		{StreamStateAcquired, StreamStateInFlight, true},
		{StreamStateAcquired, StreamStatePartial, false},
		{StreamStateAcquired, StreamStateFailed, true},
		{StreamStateInFlight, StreamStateAcquired, false},
		{StreamStateInFlight, StreamStateInFlight, true},
		{StreamStateInFlight, StreamStatePartial, true},
		{StreamStateInFlight, StreamStateFailed, true},
		{StreamStatePartial, StreamStateInFlight, false},
		{StreamStatePartial, StreamStatePartial, true},
		{StreamStatePartial, StreamStateFailed, false},
		{StreamStateFailed, StreamStateInFlight, false},
		{StreamStateFailed, StreamStatePartial, false},
		{StreamStateFailed, StreamStateFailed, true},
	}
	for _, tc := range cases {
		got := CanTransitionStreamState(tc.from, tc.to)
		if got != tc.ok {
			t.Fatalf("%s -> %s got %v want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestAttemptFromGatewayDraftPartialClientGone(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.ClientDisconnect,
		DeliveredTokenCount: 3,
	})
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount != 3 {
		t.Fatalf("delivered=%d want 3", attempt.DeliveredTokenCount)
	}
	if attempt.StreamTerminatedReason != "client_gone" {
		t.Fatalf("reason=%q want client_gone", attempt.StreamTerminatedReason)
	}
}

func TestAttemptFromGatewayDraftFailedNoCharge(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass: gateway.TotalStreamTimeout,
	})
	if attempt.State != StreamStateFailed {
		t.Fatalf("state=%s want failed", attempt.State)
	}
	if got := CostForAttempt(decimal.RequireFromString("0.01000000"), attempt); !got.IsZero() {
		t.Fatalf("failed attempt cost=%s want zero", got)
	}
	if attempt.StreamTerminatedReason != "upstream_timeout" {
		t.Fatalf("reason=%q want upstream_timeout", attempt.StreamTerminatedReason)
	}
}

// TestAttemptFromGatewayDraftCacheOnlyStreamChargeable 守 piece A:
// 成功结束(graceful)但 usage 仅含 cache 创建/读取 token、零 fresh input/output 的流,
// 之前被判 Failed → CostForAttempt 把 cache 成本归零、不写 usage_record。现应判 Partial(可计费)。
// self-proving: 同时跑 cache-only(可计费)与 no-cache(失败)两路并断言相异。
func TestAttemptFromGatewayDraftCacheOnlyStreamChargeable(t *testing.T) {
	cacheOnly := gateway.UsageRecordDraft{
		EndClass:              gateway.StreamEndGraceful,
		TokensInput:           0,
		TokensOutput:          0,
		DeliveredTokenCount:   0,
		CacheReadTokens:       512,
		CacheCreation5mTokens: 128,
	}
	attempt := AttemptFromGatewayDraft(true, cacheOnly)
	// MUTATION: state.go 把 cache-token 分支还原为无条件 StreamStateFailed → 非 chargeable → RED。
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial (cache-only graceful stream must be chargeable)", attempt.State)
	}
	cacheCost := decimal.RequireFromString("0.00200000")
	if got := CostForAttempt(cacheCost, attempt); !got.Equal(cacheCost) {
		t.Fatalf("cache-only attempt cost=%s want %s (cache cost must survive the gate)", got, cacheCost)
	}

	// 判别对照: 无任何 cache token 的同形 graceful 零-usage 流仍须判 Failed(no-billable-delivery),
	// 证明闸门没有被放宽成"所有 graceful 零-usage 都可计费"。
	noCache := cacheOnly
	noCache.CacheReadTokens = 0
	noCache.CacheCreation5mTokens = 0
	noAttempt := AttemptFromGatewayDraft(true, noCache)
	if noAttempt.State != StreamStateFailed {
		t.Fatalf("zero-usage graceful stream WITHOUT cache tokens state=%s want failed", noAttempt.State)
	}
	if got := CostForAttempt(decimal.RequireFromString("0.01000000"), noAttempt); !got.IsZero() {
		t.Fatalf("no-cache zero-usage stream cost=%s want zero", got)
	}
}

// TestAttemptFromGatewayDraftAmbiguousDeliveredChargeable 守 SM-05 闸2:
// AMBIGUOUS_USAGE 默认不可计费(进 reconciliation 留待对账),但已交付可估内容
//(EstimatedOutputTokens+EstimatedReasoningTokens>0)时须判 Partial 可计费——内容已
// 发给用户而 reconciliation 是 refund-only 永不补收,留 Failed = 永久零收漏钱。
// self-proving: 同形两 draft 仅 EstimatedOutputTokens 不同(DeliveredTokenCount 恒 40),
// 证判据是「可估输出」而非「chunk 帧数」。
func TestAttemptFromGatewayDraftAmbiguousDeliveredChargeable(t *testing.T) {
	delivered := gateway.UsageRecordDraft{
		EndClass:              gateway.AmbiguousUsage,
		EstimatedOutputTokens: 200,
		DeliveredTokenCount:   40,
	}
	attempt := AttemptFromGatewayDraft(true, delivered)
	// MUTATION: state.go 把 Ambiguous 分支还原为无条件 StreamStateFailed → 非 chargeable → RED。
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial (歧义+已交付可估内容须可计费)", attempt.State)
	}
	cost := decimal.RequireFromString("0.01000000")
	if got := CostForAttempt(cost, attempt); !got.Equal(cost) {
		t.Fatalf("ambiguous-delivered attempt cost=%s want %s (估算成本须穿过闸门落账)", got, cost)
	}

	// 判别对照: 同形但无可估交付(EstimatedOutputTokens=0)、仅 chunk 帧数 40 的歧义流仍判 Failed,
	// 证闸门没被放宽成"任何歧义+有帧数都可计费"(否则会按 chunk 帧数误收)。
	noEstimable := delivered
	noEstimable.EstimatedOutputTokens = 0
	noAttempt := AttemptFromGatewayDraft(true, noEstimable)
	if noAttempt.State != StreamStateFailed {
		t.Fatalf("无可估交付的歧义流 state=%s want failed", noAttempt.State)
	}
	if got := CostForAttempt(cost, noAttempt); !got.IsZero() {
		t.Fatalf("无可估交付歧义流 cost=%s want zero", got)
	}
}

func TestAttemptFromGatewayDraftOutputUsageWinsOverChunkFallback(t *testing.T) {
	attempt := AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.UpstreamError5xx,
		TokensOutput:        19,
		DeliveredTokenCount: 2,
	})
	if attempt.State != StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount != 19 {
		t.Fatalf("delivered=%d want authoritative output tokens 19", attempt.DeliveredTokenCount)
	}
	if got := CostForAttempt(decimal.RequireFromString("0.01000000"), attempt); got.IsZero() {
		t.Fatal("partial attempt should remain chargeable")
	}
}

func TestAttemptReasonClamped(t *testing.T) {
	long := strings.Repeat("x", 80)
	attempt := (Attempt{State: StreamStateFailed, StreamTerminatedReason: long}).Normalized()
	if len(attempt.StreamTerminatedReason) != maxStreamTerminatedReasonLen {
		t.Fatalf("reason length=%d want %d", len(attempt.StreamTerminatedReason), maxStreamTerminatedReasonLen)
	}
}

func TestAT_AUDIT_001_060_ZeroRefundReturnsSkippedCode(t *testing.T) {
	res := zeroRefundResult()
	if res == nil || res.RefundMicroUSD != 0 || res.AdjustmentRef != RefundSkippedAmountZeroRef {
		t.Fatalf("zero refund result=%+v want adjustment_ref %q", res, RefundSkippedAmountZeroRef)
	}
	if res.AdjustmentRef == "billing_refund:zero" {
		t.Fatalf("zero refund must not use ambiguous legacy ref %q", res.AdjustmentRef)
	}
}

func TestAT_AUDIT_001_062_BillingRefundCostOverflowRejected(t *testing.T) {
	_, err := costUSDToMicros(decimal.RequireFromString("9223372036854.775808"))
	if !errors.Is(err, ErrCostOverflow) {
		t.Fatalf("overflow error=%v want %v", err, ErrCostOverflow)
	}
}
