package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
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
