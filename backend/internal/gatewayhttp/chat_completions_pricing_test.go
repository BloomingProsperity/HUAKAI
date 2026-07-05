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
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/realtokenizer"
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
	// gpt-4o 属于「缓存内含」型上游:prompt_tokens(InputTokens)里已经
	// 含了 cache_read + cache_creation 这部分 token。计费会把它们从
	// input 桶里扣掉(非缓存 = 20-7-5 = 8),这样缓存 token 只计一次价,
	// 而非两次(input 费率 + cache 费率)。参见 billingUsageForCacheConvention。
	usage := proto.CanonicalUsage{
		InputTokens:              20,
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
	assertDecimalEqual(t, "official ActualCost", officialSettler.calls[0].ActualCost, decimal.RequireFromString("0.033"))
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
	assertDecimalEqual(t, "override ActualCost", overrideSettler.calls[0].ActualCost, decimal.RequireFromString("0.052"))
	assertDecimalEqual(t, "override Draft.ActualCost", overrideSettler.calls[0].Draft.ActualCost, decimal.RequireFromString("0.052"))
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

	// 变异: 若重新按 DeliveredTokenCount（此处为 40 个内容帧）计 provisional，有效费率表（output 2500）
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

	// 变异:去掉流式 draft 的 CostSnapshot 赋值,会让这里为空而成本仍正确,
	// 把审计回归藏起来。
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

	// 变异: 若错误分支无条件标 inferred（去掉 reportedUsageMissing 守卫），这条有真实 token 的行会变 inferred
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

	// SM-05 判别对照:本 draft 只有 DeliveredTokenCount=40(chunk 帧数)、无 EstimatedOutputTokens
	//(可估交付=0)。歧义放行估算的判据必须是「有可估交付内容」(EstimatedOutputTokens+
	// EstimatedReasoningTokens>0),无可估内容的歧义流仍保留 ambiguous 态留待真对账。
	// 变异: 若把放行判据错写成 DeliveredTokenCount>0(按 chunk 帧数而非可估输出),本歧义流
	// 会被误降级 inferred(且可被宽限定稿成 $0 provisional)→ UsageSource 断言 RED;
	// 正确按可估输出判据则原样保留 → GREEN。证「只在有可估交付时才收」。
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, decimal.Zero)
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceAmbiguous {
		t.Fatalf("UsageSource=%q want %q (无可估交付的歧义流须保留 ambiguous)", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceAmbiguous)
	}
	if !event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
}

func estimatedFallbackChatExecution(t *testing.T, rateTables billing.RateTableSource) *chatExecution {
	t.Helper()
	return &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables:           rateTables,
			BillingPolicyVersion: "test-policy",
		},
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13},
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		body:              []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		requestID:         "req-estimated-fallback-stream",
		reserveRes:        &billing.ReserveResult{ClaimID: 31},
		acquiredAccountID: 29,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:9;router:test"},
	}
}

func estimatedFallbackRateTable() billing.RateTableSource {
	return &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}
}

// TestStreamingCompletionEvent_WiresClientToolFromContext W4:settle draft 必须
// 带上 clientid 中间件归一出的客户端工具枚举(从请求 ctx 取),供按客户端归因
// 用量/成本。
// 变异: 去掉 streamingCompletionEvent 里 draft.ClientTool = clientToolFromContext
// 接线 → Draft.ClientTool 空 → 断言红。判别关键:ctx 注入 cursor,空 ctx 会得空串
// (区别于"恒空")。
func TestStreamingCompletionEvent_WiresClientToolFromContext(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	ex.ctx = clientid.WithIdentity(ex.ctx, clientid.IdentityCursor, 1.0)
	draft := gateway.UsageRecordDraft{
		TokensInput:  2,
		TokensOutput: 3,
		UsageSource:  gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 3}, auditledger.AuditLedgerResult{})

	if got := event.SettleRequest.Draft.ClientTool; got != "cursor" {
		t.Fatalf("Draft.ClientTool=%q want cursor(客户端归因须从 ctx 接进 settle draft)", got)
	}
}

// TestStreamingCompletionEvent_ClientToolEmptyForUnknownContext 守 W4 的 CMB-5
// 语义:未知客户端不写误导性 bucket,留空串(settle 转 NULL)。
func TestStreamingCompletionEvent_ClientToolEmptyForUnknownContext(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	ex.ctx = clientid.WithIdentity(ex.ctx, clientid.IdentityUnknown, 0.5)
	draft := gateway.UsageRecordDraft{TokensInput: 2, TokensOutput: 3, UsageSource: gateway.UsageSourceReported}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 3}, auditledger.AuditLedgerResult{})

	if got := event.SettleRequest.Draft.ClientTool; got != "" {
		t.Fatalf("Draft.ClientTool=%q want empty(未知客户端不入库误导 bucket)", got)
	}
}

func TestStreamingCompletionEvent_MissingUsageBillsEstimatedDeliveredContent(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		EstimatedOutputTokens: 200,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// 变异: 去掉估算兜底（恢复零结算路径），无 usage 但交付了 200 估算 token 的流
	// ActualCost 回到 0（漏钱）→ 本断言 RED；有兜底则按估算基数计出正成本 → GREEN。
	wantInput := tokencheck.EstimateRequestInputTokens(ex.body)
	wantCost := decimal.NewFromInt(int64(wantInput)*1000 + 200*2000).Div(decimal.NewFromInt(1_000_000))
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, wantCost)
	if got := event.SettleRequest.Draft.ConfidenceScore; got == nil || *got != estimatedUsageBasisConfidence {
		t.Fatalf("ConfidenceScore=%v want %v (estimated rows must carry degraded confidence)", got, estimatedUsageBasisConfidence)
	}
	if got := event.SettleRequest.Draft.TokensInput; got != wantInput {
		t.Fatalf("Draft.TokensInput=%d want %d (estimated basis must be recorded)", got, wantInput)
	}
	if got := event.SettleRequest.Draft.TokensOutput; got != 200 {
		t.Fatalf("Draft.TokensOutput=%d want 200 (estimated basis must be recorded)", got)
	}
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceInferred {
		t.Fatalf("UsageSource=%q want %q", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceInferred)
	}
	// 估算结算是终局：挂 pending 会让 no-usage 定稿 SQL（只认全零记录）永远跳过它。
	if event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=true want false (estimated settle is final)")
	}
	if !strings.Contains(event.SettleRequest.Draft.CostSnapshot, "usage_basis=estimated") {
		t.Fatalf("CostSnapshot=%q want usage_basis=estimated marker", event.SettleRequest.Draft.CostSnapshot)
	}
}

func TestStreamingCompletionEvent_MissingUsageEstimateIncludesReasoningText(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:      10,
		EstimatedReasoningTokens: 120,
		UsageSource:              gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 10}, auditledger.AuditLedgerResult{})

	// 变异: 估算基数漏加 EstimatedReasoningTokens，thinking-only 流（可见输出为 0）
	// 回到零结算 → 本断言 RED;计入 reasoning 文本则产出 120 token 的正成本 → GREEN。
	if got := event.SettleRequest.Draft.TokensOutput; got != 120 {
		t.Fatalf("Draft.TokensOutput=%d want 120 (reasoning text is billable output)", got)
	}
	wantInput := tokencheck.EstimateRequestInputTokens(ex.body)
	wantCost := decimal.NewFromInt(int64(wantInput)*1000 + 120*2000).Div(decimal.NewFromInt(1_000_000))
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, wantCost)
}

func TestStreamingCompletionEvent_MixedVisibleAndReasoningEstimateSums(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:      10,
		EstimatedOutputTokens:    100,
		EstimatedReasoningTokens: 120,
		UsageSource:              gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 10}, auditledger.AuditLedgerResult{})

	// 变异: 基数只取 EstimatedOutputTokens 或只取 EstimatedReasoningTokens
	//（「二选一」类变体）→ 220 断言 RED;求和则 GREEN。
	if got := event.SettleRequest.Draft.TokensOutput; got != 220 {
		t.Fatalf("Draft.TokensOutput=%d want 220 (visible + reasoning sum)", got)
	}
}

func TestStreamingCompletionEvent_ReportedUsageNeverReplacedByEstimate(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	draft := gateway.UsageRecordDraft{
		TokensInput:           10,
		TokensOutput:          1000,
		DeliveredTokenCount:   40,
		EstimatedOutputTokens: 700,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// 变异: 估算分支越过「actualCompletionCost 失败 && reportedUsageMissing」双门
	//（如 err==nil 也进估算），真实 reported usage 被估算覆盖 → 下列断言 RED。
	wantCost := decimal.NewFromInt(10*1000 + 1000*2000).Div(decimal.NewFromInt(1_000_000))
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, wantCost)
	if got := event.SettleRequest.Draft.TokensOutput; got != 1000 {
		t.Fatalf("Draft.TokensOutput=%d want 1000 (reported usage must win)", got)
	}
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceReported {
		t.Fatalf("UsageSource=%q want %q", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceReported)
	}
	if strings.Contains(event.SettleRequest.Draft.CostSnapshot, "usage_basis=") {
		t.Fatalf("CostSnapshot=%q must not carry estimated marker for reported usage", event.SettleRequest.Draft.CostSnapshot)
	}
}

func TestStreamingCompletionEvent_EstimatedSettleStripsRatioPending(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	ex.d.PricingRatioResolver = pricingcatalog.NewRatioResolver(&gatewayPricingRatioStore{err: pricingcatalog.ErrBackend}, time.Hour)
	// PoolGroupID 必须 >0:ResolveWithSignal 对非正 poolGroupID 直接返回无信号,
	// ratio fail-soft 根本不触发,本测试会退化成非判别 fixture。
	ex.attempt = router.AttemptPlan{PoolGroupID: 5}
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		EstimatedOutputTokens: 200,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// 变异: 估算路径不剥离 ratio fail-soft 带来的 PendingReconciliation → 估算行
	// 以 inferred+tokens>0+pending=true 落库,no-usage 定稿 SQL（只认全零记录）永远
	// 跳过 → 永久 pending → 本断言 RED;剥离后估算行保持终局 → GREEN（ratio 故障
	// 已由快照标记留痕，不丢审计信号）。
	if event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=true want false (estimated settle must stay final under ratio fail-soft)")
	}
	if !event.SettleRequest.ActualCost.IsPositive() {
		t.Fatalf("ActualCost=%s want positive", event.SettleRequest.ActualCost)
	}
}

func TestStreamingCompletionEvent_MultimodalInputBasisCapped(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	blob := "data:image/png;base64," + strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 2000) // ~44KB base64
	ex.body = []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"` + blob + `"}}]}]}`)
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   5,
		EstimatedOutputTokens: 50,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 5}, auditledger.AuditLedgerResult{})

	// 变异: 输入基数退回原始 body 字节数/4，44KB base64 折 ~11000 token 终局
	// 多收 → 上界断言 RED。多模态超收回归（对抗评审 S1/F-1）由此锁死。
	if got := event.SettleRequest.Draft.TokensInput; got > 2000 {
		t.Fatalf("Draft.TokensInput=%d want <= 2000 (base64 blob must be capped, not billed by raw bytes)", got)
	}
	if !event.SettleRequest.ActualCost.IsPositive() {
		t.Fatalf("ActualCost=%s want positive", event.SettleRequest.ActualCost)
	}
}

func TestStreamingCompletionEvent_AmbiguousWithDeliveredBillsEstimated(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, estimatedFallbackRateTable())
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		EstimatedOutputTokens: 200,
		UsageSource:           gateway.UsageSourceAmbiguous,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// SM-05:歧义用量但已交付可估内容(EstimatedOutputTokens>0)——内容已发给用户,而
	// reconciliation 是 refund-only/zero-finalize 永不补收,留歧义态会永久零收漏钱。故放行
	// estimatedStreamingCost 估算保守计费,升 inferred + 清 pending + 挂估算基数标记。
	// 变异: 守卫重新排除 Ambiguous(恢复零收)→ ActualCost 回零 + UsageSource 退回
	// ambiguous + pending 留 true → 下列断言全 RED;放行估算 → GREEN。判别关键:成本基数取
	// EstimatedOutputTokens=200(可见输出估算)而非 DeliveredTokenCount=40(chunk 帧数),
	// 证按可见输出估算计费而非按帧数(宁少勿多收)。
	wantInput := tokencheck.EstimateRequestInputTokens(ex.body)
	wantCost := decimal.NewFromInt(int64(wantInput)*1000 + 200*2000).Div(decimal.NewFromInt(1_000_000))
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, wantCost)
	if got := event.SettleRequest.Draft.TokensOutput; got != 200 {
		t.Fatalf("Draft.TokensOutput=%d want 200 (估算基数取可见输出 200,非 chunk 帧数 40)", got)
	}
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceInferred {
		t.Fatalf("UsageSource=%q want %q (歧义+已交付须升 inferred 落估算账)", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceInferred)
	}
	if event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=true want false (估算结算是终局)")
	}
	if !strings.Contains(event.SettleRequest.Draft.CostSnapshot, "usage_basis=estimated") {
		t.Fatalf("CostSnapshot=%q want usage_basis=estimated 标记", event.SettleRequest.Draft.CostSnapshot)
	}
}

func TestStreamingCompletionEvent_EstimateUnpriceableKeepsPendingZero(t *testing.T) {
	ex := estimatedFallbackChatExecution(t, &rateTableSourceStub{err: billing.ErrRateTableNotFound})
	draft := gateway.UsageRecordDraft{
		DeliveredTokenCount:   40,
		EstimatedOutputTokens: 200,
		UsageSource:           gateway.UsageSourceReported,
	}

	event := ex.streamingCompletionEvent(draft, billing.Attempt{DeliveredTokenCount: 40}, auditledger.AuditLedgerResult{})

	// 变异: 估算分支忽略 completionCost 错误伪造成本（或把 pending 清掉），费率表
	// 故障时会凭空收费/丢失对账信号 → RED;正确行为是回退零结算 + pending + inferred → GREEN。
	assertDecimalEqual(t, "SettleRequest.ActualCost", event.SettleRequest.ActualCost, decimal.Zero)
	if !event.SettleRequest.Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
	if event.SettleRequest.Draft.UsageSource != gateway.UsageSourceInferred {
		t.Fatalf("UsageSource=%q want %q", event.SettleRequest.Draft.UsageSource, gateway.UsageSourceInferred)
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
			// reported 远大于可见估算 → Fail20。变异:去掉 streamingCompletionEvent 的
			// crossCheckAudit stamp → ConfidenceScore 保持 nil 起始的 1.0/false → 变红。
			name:                 "fail verdict marks low confidence and pending",
			tokensOutput:         1000,
			estimatedOutput:      100,
			wantConfidence:       0.5,
			wantPendingReconcile: true,
		},
		// review R2: provider 把 thinking 以 ReasoningText 流出(estimatedReasoning>0)却
		// 不单列 ReasoningTokens(Anthropic 扩展思考 / Gemini thought)。reported OutputTokens 是否含
		// thinking 因 provider 而异、canonical 无 folding 信号 → 跳过交叉校验保持满置信、不 pending。
		// 变异:去掉 crossCheckAudit 的 `reasoningTokens==0 && estimatedReasoning>0` 跳过 →
		// visible=1000 vs estimated=100 → Fail20 → 0.5/true → 变红。
		{
			name:                 "streamed reasoning without token count suppresses cross-check",
			tokensOutput:         1000,
			estimatedOutput:      100,
			estimatedReasoning:   600,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		{
			// 隐藏 reasoning 占 reported 大头,扣除后可见==估算 → OK。变异:stamp 不传
			// draft.ReasoningTokens(传 0)→ visible=1100 vs 100 → Fail20 → 0.5/true → 变红。
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

	// 变异: stream.go 还原 `ProtocolLoss: ex.protocolLoss`(不合并 draft.StreamProtocolLoss)
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
			// 变异: rejectMoneyPathAuditRef 传 nil 而非 event.SettleRequest.ProtocolLoss → 空 → RED。
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
	// 变异: 还原 billing.go CanonicalToClientResponse 的损失丢弃(_) → settle 缺 stop_reason_unknown → RED。
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
	// 变异: 还原 dispatch.go RequestToCanonical 的损失丢弃(_) → settle 缺 d5_metadata_field_pending → RED。
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

	// 变异守卫:若把 5m 与 1h 的写入塌缩成单一 cache_creation 费率,
	// 这条精确的 0.225 cache-creation 断言会变红。
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

	// 变异守卫:若 cache_creation 桶被合并,这两个 token 数相同的用例
	// 会算出相同的 cache-creation 成本,本测试就会失败。
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
	// 变异守卫:去掉对拆分 token 的聚合兜底后,这里会返回缺失费率错误,
	// 而非沿用旧的 cache_creation 费率。
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
		// 变异:billing.go 去掉 `- usage.ReasoningTokens` 扣减 -> visible=reported -> delta=1000
		// >= 50 -> Fail20 -> 0.5/true -> 变红。
		{
			name:                 "hidden reasoning tokens excluded from cross-check keep full confidence",
			content:              blocks,
			reportedOutputTokens: estimated + 1000,
			reasoningTokens:      1000,
			actualCost:           billedCost,
			wantConfidence:       1.0,
			wantPendingReconcile: false,
		},
		// 变异守卫:若没有绝对 token 下限,这个短回复
		// 会被判成 Fail20 -> 0.5/true -> 变红。
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
		// 变异守卫:若没有零成本闸门,这个零成本 draft
		// 会被判成 Fail20 -> 0.5/true -> 变红。
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
			// 变异守卫:若 CrossCheck 未接线(confidence 硬编码 1.0、
			// pending 为 false),FAIL 用例断言 0.5/true -> 变红。
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

func (s *gatewayPricingRatioStore) VerifyChain(context.Context) (pricingcatalog.VerifyChainResult, error) {
	return pricingcatalog.VerifyChainResult{}, pricingcatalog.ErrBackend
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
// NAPI-BILLING-01 Stage A 判别性测试
// ---------------------------------------------------------------------------

// TestToolSurcharge_EmptyTableByteIdentical 验证默认关闭:当
// ToolPricingTable 为 nil 时,即便 ToolCallCounts 非零,Total 也与无附加费
// 的结果逐字节一致。
//
// 变异:若把 applyToolCallSurcharge 的调用点从 completionCost() 里移除,
// 本测试仍会通过(两条路径都跳过附加费)。
// 真正的变异守卫是下面的 TestToolSurcharge_ConfiguredFires。
func TestToolSurcharge_EmptyTableByteIdentical(t *testing.T) {
	ex := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables: &rateTableSourceStub{table: billing.RateTable{
				Version:     "test-policy",
				PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
			}},
			BillingPolicyVersion: "test-policy",
			ToolPricingTable:     nil, // 默认关闭
		},
		ident:           auth.Identity{TenantID: 7, APIKeyID: 11},
		req:             chatRequest{Model: "gpt-4o"},
		upstreamModelID: "gpt-4o",
		cacheVendor:     "openai",
	}

	// token 成本非零 + 工具调用次数非零的 usage
	usage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch: 5,
		},
	}

	// 基线:不带任何工具计价地算成本
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
		// 不带工具调用次数
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

// TestToolSurcharge_ConfiguredFires 验证:当为 (tenant, model) 配置了
// ToolPricingTable 且 WebSearch 次数非零时,附加费会正确地加进 Total。
//
// 公式:tokenCost + (WebSearchPer1000/1000 * count * groupRatio)
//
//	= tokenCost + (10.0/1000 * 3 * 1.0) = tokenCost + 0.03
//
// 变异守卫:若把 completionCost() 里 applyToolCallSurcharge 的调用点移除,
// Total 会停在 tokenCost(不加附加费)=> 本测试变红。
// 这是 Stage A 接线的主判别性测试。
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

	// 预期附加费:10.0/1000 * 3 * groupRatio(1.0) = 0.03
	// groupRatio 默认 1.0(未配置 PricingRatioResolver => ratio 0 => 当作 1 处理)
	expectedSurcharge := decimal.RequireFromString("0.03")
	wantTotal := tokenOnlyCost.Total.Add(expectedSurcharge)

	if !got.Total.Equal(wantTotal) {
		t.Fatalf("Total=%s want tokenCost(%s) + surcharge(%s) = %s MUTATION: removing applyToolCallSurcharge call site => Total stays at tokenCost => RED",
			got.Total, tokenOnlyCost.Total, expectedSurcharge, wantTotal)
	}
}

// TestToolSurcharge_PlatformSourceFiresEndToEnd 是【止漏端到端】测试,走真实生产
// 装配会注入的 toolpricing.Source —— platformSource(平台默认价 + 无 override),
// 而不是测试专用的裸 Table。它断言:WebSearch>0 时,带 source 的 Total 比无附加费的
// token-only Total【严格变大】,且差值正好是按官方默认 $10/1000 计的附加费。
//
// 这一条覆盖了字段类型从 Table 改成 Source 之后,生产真正用的那种 source 也能把
// 附加费打进 Total —— 即「漏钱被止住」。判别性 fixture:WebSearch=4 + 默认 $10/1000
// → 附加费 0.04 ≠ 0,token-only Total 与 with-surcharge Total 必不相等。
//
// 变异:把 ToolPricingTable 改成 nil(或运维开关关 → 生产注入 nil)→ 附加费不再打进
// Total、Total 退回 token-only 值,本测试 RED(严格变大断言失败)。
func TestToolSurcharge_PlatformSourceFiresEndToEnd(t *testing.T) {
	// 生产装配同款 source:平台默认价(web_search $10/1000)+ 无 override。
	source := toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), nil)

	rateTables := &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	withSurcharge := &chatExecution{
		ctx: context.Background(),
		d: ChatHandlerDeps{
			RateTables:           rateTables,
			BillingPolicyVersion: "test-policy",
			ToolPricingTable:     source,
		},
		ident:           auth.Identity{TenantID: 7, APIKeyID: 11},
		req:             chatRequest{Model: "gpt-4o"},
		upstreamModelID: "gpt-4o",
		cacheVendor:     "openai",
	}

	// 同样 token 量、同样 4 次 web_search,但 source 为 nil(模拟开关关 / 旧漏钱行为)。
	withoutSurcharge := &chatExecution{
		ctx: withSurcharge.ctx,
		d: ChatHandlerDeps{
			RateTables:           rateTables,
			BillingPolicyVersion: "test-policy",
			ToolPricingTable:     nil,
		},
		ident:           withSurcharge.ident,
		req:             withSurcharge.req,
		upstreamModelID: withSurcharge.upstreamModelID,
		cacheVendor:     withSurcharge.cacheVendor,
	}

	usage := completionUsageForCost{
		InputTokens:  100,
		OutputTokens: 200,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch: 4, // 判别性:非零次数,附加费 = 10/1000 * 4 = 0.04 ≠ 0
		},
	}

	withCost, err := withSurcharge.completionCost(usage)
	if err != nil {
		t.Fatalf("带 platformSource 计费出错: %v", err)
	}
	withoutCost, err := withoutSurcharge.completionCost(usage)
	if err != nil {
		t.Fatalf("无附加费(nil source)计费出错: %v", err)
	}

	// 1) 严格变大:带 source 的 Total 必须 > 无附加费的 Total(止漏的直接证据)。
	if !withCost.Total.GreaterThan(withoutCost.Total) {
		t.Fatalf("止漏失败:带 platformSource 的 Total(%s) 未严格大于无附加费 Total(%s) —— 工具调用仍加 $0",
			withCost.Total, withoutCost.Total)
	}

	// 2) 差值精确:附加费 = 10.0/1000 * 4 * groupRatio(1.0) = 0.04。
	gotSurcharge := withCost.Total.Sub(withoutCost.Total)
	wantSurcharge := decimal.RequireFromString("0.04")
	if !gotSurcharge.Equal(wantSurcharge) {
		t.Fatalf("附加费差值=%s want %s(web_search 4 次 @ $10/1000)", gotSurcharge, wantSurcharge)
	}
}

// estimateInputTokens 必须把请求前的估算走真实 tokenizer(默认开启),
// 这样 OpenAI 模型的预测成本 / 配额预留才会反映 tiktoken,而旧路径仍走字节估算。
// 变异:去掉 realtokenizer.Enabled() 分支(永远走 legacy),gpt-4o 断言
// 会变红;在 legacy 里恢复 (n+3)/4,则由 legacy 断言来守护它。
func TestEstimateInputTokens_RoutesThroughRealTokenizer(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"the quick brown fox jumps over the lazy dog"}]}`)

	if !realtokenizer.Enabled() {
		t.Skip("real tokenizer disabled in this environment")
	}
	got := estimateInputTokens("gpt-4o", body)
	if want := realtokenizer.InputTokens("gpt-4o", body); got != want {
		t.Fatalf("estimateInputTokens(gpt-4o)=%d; want realtokenizer %d", got, want)
	}
	// 对这段 body,真实估算必须与旧的字节估算不同,
	// 否则就观察不到接线是否生效。
	if legacy := legacyEstimateInputTokens(body); got == legacy {
		t.Fatalf("real estimate %d equals legacy %d; non-discriminating fixture", got, legacy)
	}
}

func TestLegacyEstimateInputTokens_ByteHeuristic(t *testing.T) {
	body := []byte("abcdefgh") // 8 bytes -> (8+3)/4 = 2
	if got := legacyEstimateInputTokens(body); got != 2 {
		t.Fatalf("legacyEstimateInputTokens=%d want 2", got)
	}
	if got := legacyEstimateInputTokens([]byte("   ")); got != 1 {
		t.Fatalf("legacy floor=%d want 1 for whitespace-only", got)
	}
}
