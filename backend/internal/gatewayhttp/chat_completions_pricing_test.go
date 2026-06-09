package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
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

func TestChargeUnchangedByAuditColumns(t *testing.T) {
	size := "1024x1024"
	ip := "198.51.100.9"
	ua := "huakai-audit-test/1.0"
	usage := usageFromDraft(gateway.UsageRecordDraft{
		TokensInput:        11,
		TokensOutput:       17,
		CacheReadTokens:    5,
		ImageCount:         99,
		ImageSize:          &size,
		ImageSizeBreakdown: []byte(`{"1024x1024":99}`),
		IPAddress:          &ip,
		UserAgent:          &ua,
	})

	if usage.InputTokens != 11 || usage.OutputTokens != 17 || usage.CacheReadTokens != 5 {
		t.Fatalf("usageFromDraft token projection=%+v want input=11 output=17 cache_read=5", usage)
	}
	if usage.CacheCreationTokens != 0 || usage.CacheCreation5mTokens != 0 || usage.CacheCreation1hTokens != 0 {
		t.Fatalf("usageFromDraft unexpectedly changed cache creation fields: %+v", usage)
	}
}

func TestSettleCompletion_PricingRatioBackendErrorServesAtDefaultRatioWithAlert(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.PricingRatioResolver = pricingcatalog.NewRatioResolver(&gatewayPricingRatioStore{err: pricingcatalog.ErrBackend}, time.Hour)
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}
	before := pricingcatalog.SnapshotRatioResolverSignals()

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	// 判别性夹具: 旧实现会 503 且不 settle；静默吞错则缺 metric/pending_reconciliation。
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	want := decimal.RequireFromString("0.008")
	assertDecimalEqual(t, "ActualCost", settler.calls[0].ActualCost, want)
	assertDecimalEqual(t, "Draft.ActualCost", settler.calls[0].Draft.ActualCost, want)
	if !strings.Contains(settler.calls[0].Draft.CostSnapshot, "flat") {
		t.Fatalf("Draft.CostSnapshot=%q want flat default-ratio snapshot", settler.calls[0].Draft.CostSnapshot)
	}
	if !strings.Contains(settler.calls[0].Draft.CostSnapshot, "pending_reconciliation") {
		t.Fatalf("Draft.CostSnapshot=%q want pending_reconciliation marker", settler.calls[0].Draft.CostSnapshot)
	}
	if !settler.calls[0].Draft.PendingReconciliation {
		t.Fatal("Draft.PendingReconciliation=false want true for backend-error default ratio")
	}
	after := pricingcatalog.SnapshotRatioResolverSignals()
	if after.BackendErrorWithoutLKGTotal-before.BackendErrorWithoutLKGTotal != 1 {
		t.Fatalf("backend error metric delta=%d want 1", after.BackendErrorWithoutLKGTotal-before.BackendErrorWithoutLKGTotal)
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
	if settler.calls[0].Draft.CostSnapshot != "flat" {
		t.Fatalf("Draft.CostSnapshot=%q want flat", settler.calls[0].Draft.CostSnapshot)
	}
}

func TestSettleCompletion_GroupRatioDiscountsReserveAndActualCost(t *testing.T) {
	enableHCSFDispatchForTest(t)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	baseClaimGate := &recordingClaimGate{claimID: 7001}
	baseSettler := &recordingSettler{}
	baseDeps := clientAdapterDeps(t)
	baseDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	baseDeps.ClaimGate = baseClaimGate
	baseDeps.Settler = baseSettler
	baseDeps.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}
	baseRec := invokeHandlerPath(t, baseDeps, "/v1/chat/completions", body)
	if baseRec.Code != http.StatusOK {
		t.Fatalf("baseline status=%d want 200 body=%s", baseRec.Code, baseRec.Body.String())
	}
	if len(baseSettler.calls) != 1 {
		t.Fatalf("baseline settle calls=%d want 1", len(baseSettler.calls))
	}

	discountedClaimGate := &recordingClaimGate{claimID: 7002}
	discountedSettler := &recordingSettler{}
	discountedDeps := clientAdapterDeps(t)
	discountedDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	discountedDeps.ClaimGate = discountedClaimGate
	discountedDeps.Settler = discountedSettler
	discountedDeps.PricingRatioResolver = &pricingRatioResolverStub{ratio: decimal.RequireFromString("0.8")}
	discountedDeps.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	discountedRec := invokeHandlerPath(t, discountedDeps, "/v1/chat/completions", body)
	if discountedRec.Code != http.StatusOK {
		t.Fatalf("discounted status=%d want 200 body=%s", discountedRec.Code, discountedRec.Body.String())
	}
	if len(discountedSettler.calls) != 1 {
		t.Fatalf("discounted settle calls=%d want 1", len(discountedSettler.calls))
	}

	ratio := decimal.RequireFromString("0.8")
	wantActual := baseSettler.calls[0].ActualCost.Mul(ratio)
	wantPredicted := baseClaimGate.req.PredictedCost.Mul(ratio)
	assertDecimalEqual(t, "discounted ActualCost", discountedSettler.calls[0].ActualCost, wantActual)
	assertDecimalEqual(t, "discounted Draft.ActualCost", discountedSettler.calls[0].Draft.ActualCost, wantActual)
	assertDecimalEqual(t, "discounted reserve PredictedCost", discountedClaimGate.req.PredictedCost, wantPredicted)
	if discountedSettler.calls[0].ActualCost.Equal(baseSettler.calls[0].ActualCost) {
		t.Fatalf("discounted ActualCost equals baseline %s; group ratio was not applied", baseSettler.calls[0].ActualCost)
	}
	if discountedClaimGate.req.PredictedCost.Equal(baseClaimGate.req.PredictedCost) {
		t.Fatalf("discounted PredictedCost equals baseline %s; reserve ratio was not applied", baseClaimGate.req.PredictedCost)
	}
	if !strings.Contains(discountedSettler.calls[0].Draft.CostSnapshot, "group_ratio=0.8") {
		t.Fatalf("CostSnapshot=%q want group_ratio=0.8", discountedSettler.calls[0].Draft.CostSnapshot)
	}
}

func TestSettleCompletion_CacheOverrideScalesOnlyCacheCosts(t *testing.T) {
	enableHCSFDispatchForTest(t)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	usage := proto.CanonicalUsage{
		InputTokens:              2,
		OutputTokens:             3,
		CacheCreationInputTokens: 5,
		CacheReadInputTokens:     7,
	}
	table := billing.RateTable{
		Version: "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{
			"input_micro_usd":1000,
			"output_micro_usd":2000,
			"cache_creation_micro_usd":1000,
			"cache_read_micro_usd":2000
		}}}`),
	}

	officialSettler := &recordingSettler{}
	officialDeps := clientAdapterDeps(t)
	officialDeps.CanonicalDispatcher = &cacheUsageBufferedDispatcher{usage: usage}
	officialDeps.Settler = officialSettler
	officialDeps.RateTables = &rateTableSourceStub{table: table}

	officialRec := invokeHandlerPath(t, officialDeps, "/v1/chat/completions", body)
	if officialRec.Code != http.StatusOK {
		t.Fatalf("official status=%d want 200 body=%s", officialRec.Code, officialRec.Body.String())
	}
	if len(officialSettler.calls) != 1 {
		t.Fatalf("official settle calls=%d want 1", len(officialSettler.calls))
	}
	assertDecimalEqual(t, "official ActualCost", officialSettler.calls[0].ActualCost, decimal.RequireFromString("0.027"))
	assertDecimalEqual(t, "official CacheCreationCost", officialSettler.calls[0].Draft.CacheCreationCost, decimal.RequireFromString("0.005"))
	assertDecimalEqual(t, "official CacheReadCost", officialSettler.calls[0].Draft.CacheReadCost, decimal.RequireFromString("0.014"))

	overrideResolver := &cacheOverrideResolverStub{
		tenantID:   7,
		model:      "gpt-4o",
		multiplier: decimal.NewFromInt(2),
	}
	overrideSettler := &recordingSettler{}
	overrideDeps := clientAdapterDeps(t)
	overrideDeps.CanonicalDispatcher = &cacheUsageBufferedDispatcher{usage: usage}
	overrideDeps.Settler = overrideSettler
	overrideDeps.RateTables = &rateTableSourceStub{table: table}
	overrideDeps.CacheOverrideStore = overrideResolver

	overrideRec := invokeHandlerPath(t, overrideDeps, "/v1/chat/completions", body)
	if overrideRec.Code != http.StatusOK {
		t.Fatalf("override status=%d want 200 body=%s", overrideRec.Code, overrideRec.Body.String())
	}
	if len(overrideSettler.calls) != 1 {
		t.Fatalf("override settle calls=%d want 1", len(overrideSettler.calls))
	}
	if overrideResolver.calls != 2 {
		t.Fatalf("cache override resolver calls=%d want 2 (reserve prediction + settle actual)", overrideResolver.calls)
	}
	if overrideResolver.lastTenantID != 7 || overrideResolver.lastModel != "gpt-4o" {
		t.Fatalf("cache override resolver saw tenant/model=%d/%q want 7/gpt-4o", overrideResolver.lastTenantID, overrideResolver.lastModel)
	}
	assertDecimalEqual(t, "override ActualCost", overrideSettler.calls[0].ActualCost, decimal.RequireFromString("0.046"))
	assertDecimalEqual(t, "override Draft.ActualCost", overrideSettler.calls[0].Draft.ActualCost, decimal.RequireFromString("0.046"))
	assertDecimalEqual(t, "override CacheCreationCost", overrideSettler.calls[0].Draft.CacheCreationCost, decimal.RequireFromString("0.010"))
	assertDecimalEqual(t, "override CacheReadCost", overrideSettler.calls[0].Draft.CacheReadCost, decimal.RequireFromString("0.028"))

	officialNonCache := officialSettler.calls[0].ActualCost.
		Sub(officialSettler.calls[0].Draft.CacheCreationCost).
		Sub(officialSettler.calls[0].Draft.CacheReadCost)
	overrideNonCache := overrideSettler.calls[0].ActualCost.
		Sub(overrideSettler.calls[0].Draft.CacheCreationCost).
		Sub(overrideSettler.calls[0].Draft.CacheReadCost)
	assertDecimalEqual(t, "non-cache cost after override", overrideNonCache, officialNonCache)
}

func TestSettleCompletion_UsesTieredPricingDataWhenConfigured(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version: "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{
			"pricing_model":"tiered",
			"input_micro_usd":1000,
			"output_micro_usd":2000,
			"output":[
				{"up_to_tokens":2,"rate_micro_usd":"100"},
				{"up_to_tokens":null,"rate_micro_usd":"300"}
			]
		}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	flatCost := decimal.RequireFromString("0.008")
	want := decimal.RequireFromString("0.0025")
	if settler.calls[0].ActualCost.Equal(flatCost) {
		t.Fatalf("tiered branch not used: ActualCost=%s equals flat baseline", settler.calls[0].ActualCost)
	}
	assertDecimalEqual(t, "ActualCost", settler.calls[0].ActualCost, want)
	if settler.calls[0].Draft.CostSnapshot != "tiered:vtest-policy" {
		t.Fatalf("Draft.CostSnapshot=%q want tiered:vtest-policy", settler.calls[0].Draft.CostSnapshot)
	}
}

func TestSettleCompletion_InvalidTieredPricingFallsBackToFlatAndSignals(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version: "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{
			"pricing_model":"tiered",
			"input_micro_usd":1000,
			"output_micro_usd":2000,
			"output":[
				{"up_to_tokens":3,"rate_micro_usd":"100"},
				{"up_to_tokens":2,"rate_micro_usd":"300"}
			]
		}}}`),
	}}
	before := pricingeval.SnapshotSignals().TieredFallbackTotal

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	wantFlat := decimal.RequireFromString("0.008")
	assertDecimalEqual(t, "ActualCost", settler.calls[0].ActualCost, wantFlat)
	if settler.calls[0].Draft.CostSnapshot != "flat" {
		t.Fatalf("Draft.CostSnapshot=%q want flat fallback model", settler.calls[0].Draft.CostSnapshot)
	}
	if !settler.calls[0].Draft.PendingReconciliation {
		t.Fatal("Draft.PendingReconciliation=false want true for tiered fail-soft signal")
	}
	after := pricingeval.SnapshotSignals().TieredFallbackTotal
	if after-before < 1 {
		t.Fatalf("TieredFallbackTotal delta=%d want at least 1", after-before)
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

func TestStreamingCompletionEvent_CarriesCostSnapshotToDraft(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
			}},
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		requestID:         "req-stream-cost-snapshot",
		reserveRes:        &billing.ReserveResult{ClaimID: 26},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
	draft := gateway.UsageRecordDraft{
		TokensInput:           2,
		TokensOutput:          3,
		DeliveredTokenCount:   3,
		PendingReconciliation: false,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 3}, auditledger.AuditLedgerResult{})

	// Mutation: dropping the streaming draft CostSnapshot assignment leaves
	// this empty while cost remains correct, hiding the audit regression.
	if event.SettleRequest.Draft.CostSnapshot != "flat" {
		t.Fatalf("Draft.CostSnapshot=%q want flat", event.SettleRequest.Draft.CostSnapshot)
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

// TestStreamingCompletionEvent_OutputTokenCrossCheckAnnotatesAuditFields 守 流式交叉校验:
// streamingCompletionEvent 在算出正成本后,用 forwarder 累加的可见输出估算(draft.EstimatedOutputTokens)
// 与 reported OutputTokens(扣除隐藏 reasoning)比对,把 confidence_score/pending_reconciliation 标到
// draft —— 审计-only,不改成本/usage_source。镜像非流。
func TestStreamingCompletionEvent_OutputTokenCrossCheckAnnotatesAuditFields(t *testing.T) {
	newEx := func(claimID int64) *chatExecution {
		return &chatExecution{
			ctx: context.Background(),
			d: ChatHandlerDeps{
				RateTables: &rateTableSourceStub{table: billing.RateTable{
					Version:     "test-policy",
					PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2500}}}`),
				}},
				BillingPolicyVersion: "test-policy",
			},
			ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
			req:               chatRequest{Model: "gpt-4o", Stream: true},
			requestID:         "req-stream-crosscheck",
			reserveRes:        &billing.ReserveResult{ClaimID: claimID},
			acquiredAccountID: 29,
			upstreamModelID:   "gpt-4o",
			cacheVendor:       "openai",
			plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
		}
	}

	tests := []struct {
		name                 string
		tokensOutput         int
		reasoningTokens      int
		estimatedOutput      int
		estimatedReasoning   int
		wantConfidence       float64
		wantPendingReconcile bool
	}{
		{
			// reported 远大于可见估算 → Fail20。Mutation: 去掉 streamingCompletionEvent 的
			// crossCheckAudit stamp → ConfidenceScore 保持 nil 起始的 1.0/false → RED。
			name:                 "fail verdict marks low confidence and pending",
			tokensOutput:         1000,
			estimatedOutput:      100,
			wantConfidence:       0.5,
			wantPendingReconcile: true,
		},
		// review R2: provider 把 thinking 以 ReasoningText 流出(estimatedReasoning>0)却
		// 不单列 ReasoningTokens(Anthropic 扩展思考 / Gemini thought)。reported OutputTokens 是否含
		// thinking 因 provider 而异、canonical 无 folding 信号 → 跳过交叉校验保持满置信、不 pending。
		// Mutation: 去掉 crossCheckAudit 的 `reasoningTokens==0 && estimatedReasoning>0` 跳过 →
		// visible=1000 vs estimated=100 → Fail20 → 0.5/true → RED。
		{
			name:                 "streamed reasoning without token count suppresses cross-check",
			tokensOutput:         1000,
			estimatedOutput:      100,
			estimatedReasoning:   600,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			// 隐藏 reasoning 占 reported 大头,扣除后可见==估算 → OK。Mutation: stamp 不传
			// draft.ReasoningTokens(传 0)→ visible=1100 vs 100 → Fail20 → 0.5/true → RED。
			name:                 "hidden reasoning excluded keeps full confidence",
			tokensOutput:         1100,
			reasoningTokens:      1000,
			estimatedOutput:      100,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			// 未捕获可估内容(estimated=0)→ CrossCheck 返回 Unknown → 不降级(避免假阳性)。
			name:                 "no estimate keeps full confidence",
			tokensOutput:         1000,
			estimatedOutput:      0,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			name:            "ok verdict keeps full confidence",
			tokensOutput:    100,
			estimatedOutput: 100,
			wantConfidence:  1.0,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := newEx(int64(700 + i))
			draft := gateway.UsageRecordDraft{
				TokensOutput:             tt.tokensOutput,
				ReasoningTokens:          tt.reasoningTokens,
				EstimatedOutputTokens:    tt.estimatedOutput,
				EstimatedReasoningTokens: tt.estimatedReasoning,
				DeliveredTokenCount:      int64(tt.tokensOutput),
				UsageSource:              gateway.UsageSourceReported,
			}
			event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: int64(tt.tokensOutput)}, auditledger.AuditLedgerResult{})
			got := event.SettleRequest.Draft
			if got.ActualCost.IsZero() {
				t.Fatalf("ActualCost=0 want positive (cross-check gate requires positive cost)")
			}
			if got.ConfidenceScore == nil {
				t.Fatal("ConfidenceScore=nil want populated audit score")
			}
			if *got.ConfidenceScore != tt.wantConfidence {
				t.Fatalf("ConfidenceScore=%v want %v", *got.ConfidenceScore, tt.wantConfidence)
			}
			if got.PendingReconciliation != tt.wantPendingReconcile {
				t.Fatalf("PendingReconciliation=%v want %v", got.PendingReconciliation, tt.wantPendingReconcile)
			}
			// 计费/用量口径不变:cross-check 只动审计列。
			if got.TokensOutput != tt.tokensOutput {
				t.Fatalf("TokensOutput=%d want %d unchanged", got.TokensOutput, tt.tokensOutput)
			}
			if got.UsageSource != gateway.UsageSourceReported {
				t.Fatalf("UsageSource=%q want unchanged %q", got.UsageSource, gateway.UsageSourceReported)
			}
		})
	}
}

// TestStreamingCompletionEvent_MergesRequestAndStreamProtocolLoss 守 item 4:
// 流式 settle 必须合并请求翻译损失(ex.protocolLoss)与逐事件损失(draft.StreamProtocolLoss)。
func TestStreamingCompletionEvent_MergesRequestAndStreamProtocolLoss(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
			}},
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		requestID:         "req-merge-loss",
		reserveRes:        &billing.ReserveResult{ClaimID: 31},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
	ex.protocolLoss = json.RawMessage(`[{"severity":"info","code":"request_translation_loss_sentinel","reason":"request translation"}]`)
	draft := gateway.UsageRecordDraft{
		TokensInput:  10,
		TokensOutput: 20,
		StreamProtocolLoss: []proto.ProtocolLossEntry{
			{Severity: proto.ProtocolLossWarning, Code: "stream_event_loss_sentinel", Reason: "provider/client stream event"},
		},
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 20}, auditledger.AuditLedgerResult{})

	// MUTATION: stream.go 还原 `ProtocolLoss: ex.protocolLoss`(不合并 draft.StreamProtocolLoss)
	// → 缺 stream_event_loss_sentinel → RED。
	if !settledLossHasCode(t, event.SettleRequest.ProtocolLoss, "request_translation_loss_sentinel") {
		t.Fatalf("merged ProtocolLoss missing request sentinel: %s", event.SettleRequest.ProtocolLoss)
	}
	if !settledLossHasCode(t, event.SettleRequest.ProtocolLoss, "stream_event_loss_sentinel") {
		t.Fatalf("merged ProtocolLoss missing stream sentinel: %s", event.SettleRequest.ProtocolLoss)
	}
}

// TestRejectMoneyPathAuditRef_PreservesEventProtocolLoss 守 item 3:
// audit-ref-missing 的零成本 abort 必须带上 event.SettleRequest.ProtocolLoss(而非 nil)。
func TestRejectMoneyPathAuditRef_PreservesEventProtocolLoss(t *testing.T) {
	sentinel := json.RawMessage(`[{"severity":"warning","code":"audit_ref_abort_sentinel","reason":"audit-ref reject must preserve losses"}]`)
	event := eventbus.RequestCompletionEvent{
		TenantID:      7,
		ClaimID:       9,
		RequestID:     "req-audit-ref-loss",
		SettleRequest: billing.SettleRequest{ProtocolLoss: sentinel},
	}
	cases := []struct {
		name string
		call func(d ChatHandlerDeps)
	}{
		{"direct_settle", func(d ChatHandlerDeps) {
			_ = rejectMoneyPathDirectSettle(context.Background(), d, event, eventbus.ErrAuditRefMissing)
		}},
		{"cache_hit_commit", func(d ChatHandlerDeps) {
			_, _ = rejectMoneyPathCacheHitCommit(context.Background(), d, event, eventbus.ErrAuditRefMissing)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settler := &recordingSettler{}
			tc.call(ChatHandlerDeps{Settler: settler})
			if len(settler.aborts) != 1 {
				t.Fatalf("aborts=%d want 1", len(settler.aborts))
			}
			// MUTATION: rejectMoneyPathAuditRef 传 nil 而非 event.SettleRequest.ProtocolLoss → 空 → RED。
			if !settledLossHasCode(t, settler.aborts[0].protocolLoss, "audit_ref_abort_sentinel") {
				t.Fatalf("abort protocolLoss=%s want code audit_ref_abort_sentinel", settler.aborts[0].protocolLoss)
			}
		})
	}
}

// TestNonStreamingSettle_CapturesResponseConversionProtocolLoss 守 item 2(c):
// CanonicalToClientResponse 的损失返回之前被丢弃;StopUnknown → stop_reason_unknown 必须进 settle。
func TestNonStreamingSettle_CapturesResponseConversionProtocolLoss(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &protocolLossTestDispatcher{stopReason: proto.CanonicalStopUnknown}
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
	// MUTATION: 还原 billing.go CanonicalToClientResponse 的损失丢弃(_) → settle 缺 stop_reason_unknown → RED。
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "stop_reason_unknown") {
		t.Fatalf("settle ProtocolLoss=%s want code stop_reason_unknown", settler.calls[0].ProtocolLoss)
	}
}

// TestNonStreamingSettle_CapturesRequestTranslationProtocolLoss 守 item 2(a):
// RequestToCanonical 的损失之前在非流式 buffered 路径整段丢弃;请求体带 metadata →
// d5_metadata_field_pending 必须经 cloneHCSF 进 settle(preserveRequestLoss 模拟真实克隆)。
func TestNonStreamingSettle_CapturesRequestTranslationProtocolLoss(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &protocolLossTestDispatcher{preserveRequestLoss: true}
	d.Settler = settler
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"metadata":{"trace":"x"},"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	// MUTATION: 还原 dispatch.go RequestToCanonical 的损失丢弃(_) → settle 缺 d5_metadata_field_pending → RED。
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "d5_metadata_field_pending") {
		t.Fatalf("settle ProtocolLoss=%s want code d5_metadata_field_pending", settler.calls[0].ProtocolLoss)
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
		reasoningTokens      int
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
		// 判别: o1/o3 隐藏 reasoning token 占 reported OutputTokens 大头。reported
		// = 可见估算 + 1000 reasoning；扣除 reasoning 后 visible == estimated -> OK -> 满信心。
		// Mutation: billing.go 去掉 `- usage.ReasoningTokens` 扣减 -> visible=reported -> delta=1000
		// >= 50 -> Fail20 -> 0.5/true -> RED。
		{
			name:                 "hidden reasoning tokens excluded from cross-check keep full confidence",
			content:              blocks,
			reportedOutputTokens: estimated + 1000,
			reasoningTokens:      1000,
			actualCost:           billedCost,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
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
				Usage:   proto.CanonicalUsage{OutputTokens: tt.reportedOutputTokens, ReasoningTokens: tt.reasoningTokens},
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

type cacheUsageBufferedDispatcher struct {
	usage proto.CanonicalUsage
}

func (m *cacheUsageBufferedDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-cache-override-test",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "cache priced response"}},
		Usage:      m.usage,
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
}

type cacheOverrideResolverStub struct {
	tenantID     int64
	model        string
	multiplier   decimal.Decimal
	calls        int
	lastTenantID int64
	lastModel    string
}

func (s *cacheOverrideResolverStub) ResolveMultiplier(tenantID int64, model string) decimal.Decimal {
	s.calls++
	s.lastTenantID = tenantID
	s.lastModel = model
	if s != nil && tenantID == s.tenantID && model == s.model {
		return s.multiplier
	}
	return decimal.NewFromInt(1)
}

type pricingRatioResolverStub struct {
	ratio decimal.Decimal
	err   error
}

type gatewayPricingRatioStore struct {
	err error
}

func (s *gatewayPricingRatioStore) GetRatio(context.Context, int64, int64) (pricingcatalog.GroupPricingRatio, error) {
	if s != nil && s.err != nil {
		return pricingcatalog.GroupPricingRatio{}, s.err
	}
	return pricingcatalog.GroupPricingRatio{}, pricingcatalog.ErrNotFound
}

func (s *gatewayPricingRatioStore) ListRatios(context.Context, int64) ([]pricingcatalog.GroupPricingRatio, error) {
	return nil, pricingcatalog.ErrBackend
}

func (s *gatewayPricingRatioStore) UpsertRatio(context.Context, pricingcatalog.UpsertRatioParams) (pricingcatalog.GroupPricingRatio, error) {
	return pricingcatalog.GroupPricingRatio{}, pricingcatalog.ErrBackend
}

func (s *gatewayPricingRatioStore) DeleteRatio(context.Context, pricingcatalog.DeleteRatioParams) error {
	return pricingcatalog.ErrBackend
}

func (s *pricingRatioResolverStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s != nil && s.err != nil {
		return decimal.Zero, s.err
	}
	if s == nil || s.ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return s.ratio, nil
}

// ---------------------------------------------------------------------------
// NAPI-BILLING-01 Stage A discriminating tests
// ---------------------------------------------------------------------------

// TestToolSurcharge_EmptyTableByteIdentical verifies default-off: when
// ToolPricingTable is nil, the Total is byte-identical to the no-surcharge
// result even if ToolCallCounts are non-zero.
//
// MUTATION: if the applyToolCallSurcharge call site is removed from
// completionCost(), this test still passes (both paths skip surcharge).
// The mutation guard is TestToolSurcharge_ConfiguredFires below.
func TestToolSurcharge_EmptyTableByteIdentical(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
			}},
			BillingPolicyVersion: "test-policy",
			ToolPricingTable:     nil, // default-off
		},
		ident:           auth.Identity{TenantID: 7, APIKeyID: 11},
		req:             chatRequest{Model: "gpt-4o"},
		upstreamModelID: "gpt-4o",
		cacheVendor:     "openai",
	}

	// usage with non-zero token cost + non-zero tool calls
	usage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch: 5,
		},
	}

	// baseline: compute cost without any tool pricing
	baseEx := &chatExecution{
		ctx: ex.ctx,
		d: ChatHandlerDeps{
			RateTables:           ex.d.RateTables,
			BillingPolicyVersion: ex.d.BillingPolicyVersion,
			ToolPricingTable:     nil,
		},
		ident:           ex.ident,
		req:             ex.req,
		upstreamModelID: ex.upstreamModelID,
		cacheVendor:     ex.cacheVendor,
	}
	baselineUsage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
		// no tool counts
	}

	got, err := ex.completionCost(usage)
	if err != nil {
		t.Fatalf("completionCost error: %v", err)
	}
	want, err := baseEx.completionCost(baselineUsage)
	if err != nil {
		t.Fatalf("baseline completionCost error: %v", err)
	}

	if !got.Total.Equal(want.Total) {
		t.Fatalf("nil ToolPricingTable: Total=%s want byte-identical %s (default-off violated)", got.Total, want.Total)
	}
}

// TestToolSurcharge_ConfiguredFires verifies that when a ToolPricingTable is
// configured for (tenant, model) and WebSearch counts are non-zero, the
// surcharge is correctly added to Total.
//
// Formula: tokenCost + (WebSearchPer1000/1000 * count * groupRatio)
//
//	= tokenCost + (10.0/1000 * 3 * 1.0) = tokenCost + 0.03
//
// MUTATION GUARD: if the applyToolCallSurcharge call site in completionCost()
// is removed, Total stays at tokenCost (no surcharge added) => this test RED.
// This is the primary discriminating test for Stage A wiring.
func TestToolSurcharge_ConfiguredFires(t *testing.T) {
	priceTable := toolpricing.Table{}
	priceTable.Set(7, "gpt-4o", toolpricing.ToolPrices{
		WebSearchPer1000: decimal.RequireFromString("10"),
	})

	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
			}},
			BillingPolicyVersion: "test-policy",
			ToolPricingTable:     priceTable,
		},
		ident:           auth.Identity{TenantID: 7, APIKeyID: 11},
		req:             chatRequest{Model: "gpt-4o"},
		upstreamModelID: "gpt-4o",
		cacheVendor:     "openai",
	}

	tokenOnlyUsage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
	}
	tokenOnlyCost, err := (&chatExecution{
		ctx: ex.ctx,
		d: ChatHandlerDeps{
			RateTables:           ex.d.RateTables,
			BillingPolicyVersion: ex.d.BillingPolicyVersion,
			ToolPricingTable:     nil,
		},
		ident:           ex.ident,
		req:             ex.req,
		upstreamModelID: ex.upstreamModelID,
		cacheVendor:     ex.cacheVendor,
	}).completionCost(tokenOnlyUsage)
	if err != nil {
		t.Fatalf("token-only cost error: %v", err)
	}

	usage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch: 3,
		},
	}

	got, err := ex.completionCost(usage)
	if err != nil {
		t.Fatalf("completionCost with surcharge error: %v", err)
	}

	// Expected surcharge: 10.0/1000 * 3 * groupRatio(1.0) = 0.03
	// groupRatio defaults to 1.0 (no PricingRatioResolver configured => ratio 0 => treated as 1)
	expectedSurcharge := decimal.RequireFromString("0.03")
	wantTotal := tokenOnlyCost.Total.Add(expectedSurcharge)

	if !got.Total.Equal(wantTotal) {
		t.Fatalf("Total=%s want tokenCost(%s) + surcharge(%s) = %s MUTATION: removing applyToolCallSurcharge call site => Total stays at tokenCost => RED",
			got.Total, tokenOnlyCost.Total, expectedSurcharge, wantTotal)
	}
}
