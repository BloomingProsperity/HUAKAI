package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

func TestSettleCompletion_RateTableMiss_FailsClosed(t *testing.T) {
	enableHCSFDispatchForTest(t)
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.RateTables = &rateTableSourceStub{err: billing.ErrRateTableNotFound}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pricing_unavailable") {
		t.Fatalf("body=%s want pricing_unavailable", rec.Body.String())
	}
}

func TestSettleCompletion_UsesRateTableActualCost(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	want := decimal.RequireFromString("0.008")
	if !settler.calls[0].ActualCost.Equal(want) {
		t.Fatalf("ActualCost=%s want %s", settler.calls[0].ActualCost, want)
	}
	if !settler.calls[0].Draft.ActualCost.Equal(want) {
		t.Fatalf("Draft.ActualCost=%s want %s", settler.calls[0].Draft.ActualCost, want)
	}
}

func TestStreamingCompletionEvent_NoUsageKeepsZeroCostPendingInferred(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":999999,"output_micro_usd":2500}}}`),
			}},
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		requestID:         "req-provisional-stream",
		reserveRes:        &billing.ReserveResult{ClaimID: 23},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		PendingReconciliation: false,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// MUTATION: 若重新按 DeliveredTokenCount（此处为 40 个内容帧）计 provisional，有效费率表（output 2500）
	// 会把 40 帧当 40 token 计成 ActualCost=0.1（向用户多收）→ 此断言（==0）RED；修复后帧数不计费 → 0 → GREEN。
	// 有效费率表是判别关键：费率表损坏会让新旧都为 0（非判别）。
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, decimal.Zero)
	assertDecimalEqual(t, "Draft.ActualCost", event.SettleRequest.Draft.ActualCost, decimal.Zero)
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceInferred {
		t.Fatalf("UsageSource=%q want %q", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceInferred)
	}
	if !event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
}

func TestStreamingCompletionEvent_PricingConfigFailureStaysReportedNotInferred(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables:           &rateTableSourceStub{err: billing.ErrRateTableNotFound},
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		requestID:         "req-config-failure-stream",
		reserveRes:        &billing.ReserveResult{ClaimID: 24},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
	draft := gateway.UsageRecordDraft{
		TokensInput:           10,
		TokensOutput:          100,
		DeliveredTokenCount:   40,
		PendingReconciliation: false,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// MUTATION: 若错误分支无条件标 inferred（去掉 reportedUsageMissing 守卫），这条有真实 token 的行会变 inferred
	// → settlementreconcile worker 会把真实请求零差额定稿成 $0（静默零计费）→ RED；有守卫则保持 reported（留人工对账）→ GREEN。
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, decimal.Zero)
	if !event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceReported {
		t.Fatalf("UsageSource=%q want %q", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceReported)
	}
}

func TestStreamingCompletionEvent_AmbiguousUsagePreservedNotInferred(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":999999,"output_micro_usd":2500}}}`),
			}},
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		requestID:         "req-ambiguous-stream",
		reserveRes:        &billing.ReserveResult{ClaimID: 25},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		PendingReconciliation: false,
		UsageSource:           gateway.UsageSourceAmbiguous,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// MUTATION: 去掉 `!= UsageSourceAmbiguous` 守卫，歧义用量会被降级成 inferred → RED。
	// 歧义流须保留 ambiguous 态留待真对账，不可降级成可被宽限定稿的 $0 provisional。
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, decimal.Zero)
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceAmbiguous {
		t.Fatalf("UsageSource=%q want %q (ambiguous must be preserved)", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceAmbiguous)
	}
	if !event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
}

func TestCompletionRateVector_PricesCacheCreationTiersAndReadSeparately(t *testing.T) {
	rates, err := parseRateVector(json.RawMessage(`{
		"input_micro_usd":1000,
		"output_micro_usd":2000,
		"cache_creation_5m_micro_usd":1250,
		"cache_creation_1h_micro_usd":2000,
		"cache_read_micro_usd":100,
		"model_multiplier":1
	}`))
	if err != nil {
		t.Fatalf("parseRateVector: %v", err)
	}

	got, err := rates.price(completionUsageForCost{
		CacheCreation5mTokens: 100,
		CacheCreation1hTokens: 50,
		CacheReadTokens:       200,
	})
	if err != nil {
		t.Fatalf("price: %v", err)
	}

	// Mutation guard: if 5m and 1h writes are collapsed to one cache_creation rate,
	// this exact 0.225 cache-creation assertion goes red.
	assertDecimalEqual(t, "CacheCreationCost", got.CacheCreationCost, decimal.RequireFromString("0.225"))
	assertDecimalEqual(t, "CacheReadCost", got.CacheReadCost, decimal.RequireFromString("0.02"))
	assertDecimalEqual(t, "Total", got.Total, decimal.RequireFromString("0.245"))
}

func TestCompletionRateVector_CacheCreationSplitChangesCostForSameTokenTotal(t *testing.T) {
	rates, err := parseRateVector(json.RawMessage(`{
		"cache_creation_5m_micro_usd":1250,
		"cache_creation_1h_micro_usd":2000
	}`))
	if err != nil {
		t.Fatalf("parseRateVector: %v", err)
	}

	fiveMinuteOnly, err := rates.price(completionUsageForCost{CacheCreation5mTokens: 100})
	if err != nil {
		t.Fatalf("price 5m-only: %v", err)
	}
	oneHourOnly, err := rates.price(completionUsageForCost{CacheCreation1hTokens: 100})
	if err != nil {
		t.Fatalf("price 1h-only: %v", err)
	}

	// Mutation guard: with a merged cache_creation bucket, these equal-token cases
	// would produce the same cache-creation cost and this test would fail.
	if fiveMinuteOnly.CacheCreationCost.Equal(oneHourOnly.CacheCreationCost) {
		t.Fatalf("same cache token total with different TTL split must price differently; 5m=%s 1h=%s",
			fiveMinuteOnly.CacheCreationCost, oneHourOnly.CacheCreationCost)
	}
	assertDecimalEqual(t, "5m CacheCreationCost", fiveMinuteOnly.CacheCreationCost, decimal.RequireFromString("0.125"))
	assertDecimalEqual(t, "1h CacheCreationCost", oneHourOnly.CacheCreationCost, decimal.RequireFromString("0.2"))
}

func TestCompletionRateVector_UsesAggregateCacheCreationFallback(t *testing.T) {
	rates, err := parseRateVector(json.RawMessage(`{
		"cache_creation_micro_usd":1500
	}`))
	if err != nil {
		t.Fatalf("parseRateVector: %v", err)
	}

	aggregateOnly, err := rates.price(completionUsageForCost{CacheCreationTokens: 30})
	if err != nil {
		t.Fatalf("price aggregate-only: %v", err)
	}
	assertDecimalEqual(t, "aggregate CacheCreationCost", aggregateOnly.CacheCreationCost, decimal.RequireFromString("0.045"))
	assertDecimalEqual(t, "aggregate Total", aggregateOnly.Total, decimal.RequireFromString("0.045"))

	splitFallback, err := rates.price(completionUsageForCost{
		CacheCreation5mTokens: 10,
		CacheCreation1hTokens: 20,
	})
	if err != nil {
		t.Fatalf("price split fallback: %v", err)
	}
	// Mutation guard: removing the aggregate fallback for split tokens makes this
	// return a missing-rate error instead of the legacy cache_creation rate.
	assertDecimalEqual(t, "split fallback CacheCreationCost", splitFallback.CacheCreationCost, decimal.RequireFromString("0.045"))
	assertDecimalEqual(t, "split fallback Total", splitFallback.Total, decimal.RequireFromString("0.045"))
}

func TestNonStreamingUsageDraft_OutputTokenCrossCheckAnnotatesAuditFields(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: "hello world"}}
	estimated := tokencheck.HeuristicEstimator{}.Estimate(blocks)
	if estimated <= 0 {
		t.Fatalf("test fixture estimate=%d want positive", estimated)
	}
	shortBlocks := []proto.CanonicalContentBlock{{Type: "text", Text: "hello"}}
	shortEstimated := tokencheck.HeuristicEstimator{}.Estimate(shortBlocks)
	if shortEstimated <= 0 {
		t.Fatalf("short test fixture estimate=%d want positive", shortEstimated)
	}
	billedCost := completionCostBreakdown{Total: decimal.RequireFromString("0.01")}

	tests := []struct {
		name                 string
		content              []proto.CanonicalContentBlock
		reportedOutputTokens int
		actualCost           completionCostBreakdown
		wantConfidence       float64
		wantPendingReconcile bool
	}{
		{
			name:                 "fail verdict marks low confidence and pending reconciliation",
			content:              blocks,
			reportedOutputTokens: 1000,
			actualCost:           billedCost,
			wantConfidence:       0.5,
			wantPendingReconcile: true,
		},
		// Mutation guard: without the absolute-token floor, this short
		// response would be Fail20 -> 0.5/true -> RED.
		{
			name:                 "short fail verdict below absolute floor keeps full confidence",
			content:              shortBlocks,
			reportedOutputTokens: shortEstimated + 2,
			actualCost:           billedCost,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			name:                 "ok verdict keeps full confidence",
			content:              blocks,
			reportedOutputTokens: estimated,
			actualCost:           billedCost,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			name:                 "unknown verdict keeps safe default confidence",
			content:              nil,
			reportedOutputTokens: 0,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		// Mutation guard: without the zero-cost gate, this zero-cost draft
		// would be Fail20 -> 0.5/true -> RED.
		{
			name:                 "zero cost fail verdict keeps full confidence",
			content:              blocks,
			reportedOutputTokens: 1000,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := proto.NewEmptyEnvelope()
			env.BufferedResponse = &proto.CanonicalResponse{
				Content: tt.content,
				Usage:   proto.CanonicalUsage{OutputTokens: tt.reportedOutputTokens},
			}

			draft := nonStreamingUsageDraft(env, tt.actualCost, nil)
			if draft.ConfidenceScore == nil {
				t.Fatal("ConfidenceScore=nil want populated audit score")
			}
			// Mutation guard: if CrossCheck is not wired (confidence hardcoded 1.0,
			// pending false), the FAIL case asserts 0.5/true -> RED.
			if got := *draft.ConfidenceScore; got != tt.wantConfidence {
				t.Fatalf("ConfidenceScore=%v want %v", got, tt.wantConfidence)
			}
			if draft.PendingReconciliation != tt.wantPendingReconcile {
				t.Fatalf("PendingReconciliation=%v want %v", draft.PendingReconciliation, tt.wantPendingReconcile)
			}
			if draft.UsageSource != gateway.UsageSourceReported {
				t.Fatalf("UsageSource=%q want unchanged %q", draft.UsageSource, gateway.UsageSourceReported)
			}
			if draft.TokensOutput != tt.reportedOutputTokens {
				t.Fatalf("TokensOutput=%d want reported output tokens unchanged %d", draft.TokensOutput, tt.reportedOutputTokens)
			}
		})
	}
}

func assertDecimalEqual(t *testing.T, field string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s=%s want %s", field, got, want)
	}
}
