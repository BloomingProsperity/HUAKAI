package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

var errCompletionPricingUnavailable = errors.New("gatewayhttp: pricing unavailable")

type completionUsageForCost struct {
	InputTokens           int
	OutputTokens          int
	CacheCreationTokens   int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	CacheReadTokens       int

	// ToolCallCounts holds built-in tool call counts for surcharge billing.
	// Defaults to zero (no surcharge) when not populated.
	//
	// TODO(NAPI-BILLING-01): wire real counts from upstream response usage.
	// Provider-specific locations where tool-call counts are exposed:
	//   - OpenAI chat/completions: response.usage has no per-tool call count
	//     field today; OpenAI bills via usage_details once GA.
	//   - OpenAI Responses API: response.usage.input_tokens_details may carry
	//     tool call counts in future versions.
	//   - Anthropic Messages API: server_tool_use block count can be derived
	//     from response.content blocks of type="server_tool_use"; no dedicated
	//     usage field exists today.
	// Until a stable upstream signal is available, counts default to zero,
	// which means zero surcharge — safe conservative billing.
	ToolCallCounts toolpricing.ToolCallCounts
}

type completionRateVector struct {
	Input           decimal.Decimal
	Output          decimal.Decimal
	CacheCreation   decimal.Decimal
	CacheCreation5m decimal.Decimal
	CacheCreation1h decimal.Decimal
	CacheRead       decimal.Decimal
	Multiplier      decimal.Decimal

	HasInput           bool
	HasOutput          bool
	HasCacheCreation   bool
	HasCacheCreation5m bool
	HasCacheCreation1h bool
	HasCacheRead       bool
}

type completionCostBreakdown struct {
	Total                 decimal.Decimal
	CacheCreationCost     decimal.Decimal
	CacheReadCost         decimal.Decimal
	CostSnapshot          string
	PendingReconciliation bool
}

type completionPricingSelection struct {
	Raw   json.RawMessage
	Rates completionRateVector
}

type pricingRatioResolverWithSignal interface {
	ResolveWithSignal(ctx context.Context, tenantID, poolGroupID int64) (decimal.Decimal, bool, error)
}

func (ex *chatExecution) predictedCompletionCost() (decimal.Decimal, error) {
	cost, err := ex.completionCost(completionUsageForCost{
		InputTokens:  estimateInputTokens(ex.body),
		OutputTokens: estimateOutputTokens(ex.req),
	})
	if err != nil {
		return decimal.Zero, err
	}
	return cost.Total, nil
}

func reportedUsageMissing(u completionUsageForCost) bool {
	return u.InputTokens <= 0 && u.OutputTokens <= 0 &&
		u.CacheCreationTokens <= 0 && u.CacheCreation5mTokens <= 0 &&
		u.CacheCreation1hTokens <= 0 && u.CacheReadTokens <= 0
}

func (ex *chatExecution) actualCompletionCost(usage completionUsageForCost) (completionCostBreakdown, error) {
	if reportedUsageMissing(usage) {
		return completionCostBreakdown{}, pricingUnavailable("reported usage missing")
	}
	return ex.completionCost(usage)
}

// estimatedUsageBasisMarker 标记该笔结算的 token 基数来自交付内容估算而非上游
// 报告,与 usage_source=inferred 配合构成估算计费的审计链。在 usage_source 枚举
// 的 'estimated' 值(需 schema 迁移,已 park 待 Owner)落地前,以本标记区分估算行
// 与真实 usage 的 inferred 行。
const estimatedUsageBasisMarker = "usage_basis=estimated_from_delivered_content"

// estimatedUsageBasisConfidence 是估算计费行的固定置信:估算行的 token 交叉校验
// 是自比对(恒满置信)而无意义,改记降级常量保留「基数有不确定性」的审计信号。
const estimatedUsageBasisConfidence = 0.8

// estimatedStreamingCost 在上游全程未报告 usage 的流上,用 forwarder 逐事件累积的
// 可见内容估算作 token 基数计价。输出基数 = EstimatedOutputTokens +
// EstimatedReasoningTokens(可见 reasoning 文本对上游同样是产出 token,漏加会系统性
// 低估 thinking 流);输入基数走协议无关的内容感知估算(tokencheck,base64 大块
// 封顶)——不可用原始 body 字节数/4,多模态请求会按 base64 体积百倍超收。
// 估算结算是终局:权威 usage 永远不会到达,挂 pending 只会让 no-usage 定稿 SQL
// (只认 tokens 与 actual_cost 全零的记录)永远跳过它;故连 ratio fail-soft 的
// pending 也剥离(其快照标记已留痕)。无可估内容或费率表不可用时返回 ok=false,
// 调用方维持零结算 + pending 的原路径。
func (ex *chatExecution) estimatedStreamingCost(draft gateway.UsageRecordDraft) (completionCostBreakdown, completionUsageForCost, bool) {
	estimatedOutput := draft.EstimatedOutputTokens + draft.EstimatedReasoningTokens
	if estimatedOutput <= 0 {
		return completionCostBreakdown{}, completionUsageForCost{}, false
	}
	usage := completionUsageForCost{
		InputTokens:  tokencheck.EstimateRequestInputTokens(ex.body),
		OutputTokens: estimatedOutput,
	}
	cost, err := ex.completionCost(usage)
	if err != nil {
		return completionCostBreakdown{}, completionUsageForCost{}, false
	}
	cost.PendingReconciliation = false
	cost.CostSnapshot = snapshotWithEstimatedUsageBasis(cost.CostSnapshot)
	return cost, usage, true
}

func snapshotWithEstimatedUsageBasis(snapshot string) string {
	if strings.TrimSpace(snapshot) == "" {
		return estimatedUsageBasisMarker
	}
	if strings.Contains(snapshot, "usage_basis=") {
		return snapshot
	}
	return snapshot + ";" + estimatedUsageBasisMarker
}

// cacheExclusiveInputFamilies are upstream protocol families whose vendor reports
// input_tokens EXCLUDING cache-read/cache-creation tokens (cache counted as a
// parallel dimension, per the Anthropic Messages contract). For these the additive
// rate model already prices each dimension once. Every other family (OpenAI, Gemini,
// and all OpenAI-compatible providers) reports prompt_tokens INCLUDING cached tokens,
// so cached tokens must be removed from the billing input bucket to avoid charging
// them twice -- once at the input rate and again at the cache rate. Mirrors new-api's
// `!IsClaudeUsageSemantic` base-token subtraction (service/text_quota.go).
var cacheExclusiveInputFamilies = map[string]struct{}{
	"anthropic_messages": {},
	"bedrock_invoke":     {},
}

func inputTokensExcludeCache(protocolFamily string) bool {
	_, ok := cacheExclusiveInputFamilies[strings.TrimSpace(protocolFamily)]
	return ok
}

// billingUsageForCacheConvention returns a billing copy of usage whose InputTokens
// never double-counts cached tokens. Cache-inclusive upstreams fold cache-read and
// cache-creation tokens into prompt_tokens; subtract them so the input bucket bills
// only non-cached tokens while the cache buckets bill the cached tokens once.
// Client-facing CanonicalUsage is untouched; only this billing-local copy changes.
func (ex *chatExecution) billingUsageForCacheConvention(usage completionUsageForCost) completionUsageForCost {
	if inputTokensExcludeCache(ex.resolved.ProtocolFamily) {
		return usage
	}
	nonCached := usage.InputTokens - usage.CacheReadTokens - usage.CacheCreationTokens
	if nonCached < 0 {
		nonCached = 0
	}
	usage.InputTokens = nonCached
	return usage
}

func (ex *chatExecution) completionCost(usage completionUsageForCost) (completionCostBreakdown, error) {
	usage = ex.billingUsageForCacheConvention(usage)
	if ex.d.RateTables == nil {
		return completionCostBreakdown{}, pricingUnavailable("rate table source not configured")
	}
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		return completionCostBreakdown{}, pricingUnavailable("billing policy version empty")
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		if errors.Is(err, billing.ErrRateTableNotFound) {
			return completionCostBreakdown{}, pricingUnavailable(fmt.Sprintf("rate table version %q not found", version))
		}
		return completionCostBreakdown{}, pricingUnavailable(fmt.Sprintf("rate table version %q read failed: %v", version, err))
	}
	selection, err := rateVectorFromTable(table.PricingData, ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return completionCostBreakdown{}, err
	}
	fallback := selection.Rates.flatRateFallback()
	groupRatio, ratioPendingReconciliation := ex.groupPricingRatio()
	fallback.GroupRatio = groupRatio
	result, err := pricingeval.Resolve(ex.ctx, selection.Raw, pricingUsage(usage), fallback, version)
	if err != nil {
		return completionCostBreakdown{}, pricingUnavailable(err.Error())
	}
	result = ex.applyCacheCostOverride(result)
	result = ex.applyToolCallSurcharge(result, usage.ToolCallCounts, groupRatio)
	if ratioPendingReconciliation {
		result.PendingReconciliation = true
		result.CostSnapshot = snapshotWithPricingRatioPending(result.CostSnapshot)
	}
	return completionCostBreakdown{
		Total:                 result.Total,
		CacheCreationCost:     result.CacheCreationCost,
		CacheReadCost:         result.CacheReadCost,
		CostSnapshot:          result.CostSnapshot,
		PendingReconciliation: result.PendingReconciliation,
	}, nil
}

func (ex *chatExecution) applyCacheCostOverride(result pricingeval.Result) pricingeval.Result {
	if ex == nil || ex.d.CacheOverrideStore == nil {
		return result
	}
	override := pricingeval.CacheCostOverride{
		Multiplier: ex.d.CacheOverrideStore.ResolveMultiplier(ex.ident.TenantID, ex.req.Model),
	}
	if override.IsIdentity() {
		return result
	}
	return pricingeval.ApplyCacheCostOverride(result, override)
}

// applyToolCallSurcharge adds the built-in tool-call surcharge to result when a
// ToolPricingTable is configured for this (tenant, model) pair. Returns result
// unmodified (default-off) when ToolPricingTable is nil or the lookup returns
// zero prices.
func (ex *chatExecution) applyToolCallSurcharge(result pricingeval.Result, counts toolpricing.ToolCallCounts, groupRatio decimal.Decimal) pricingeval.Result {
	if ex.d.ToolPricingTable == nil {
		return result
	}
	prices := ex.d.ToolPricingTable.Lookup(ex.ident.TenantID, ex.req.Model)
	return pricingeval.ApplyToolCallSurcharge(result, prices, counts, groupRatio)
}

func (ex *chatExecution) groupPricingRatio() (decimal.Decimal, bool) {
	if ex == nil || ex.d.PricingRatioResolver == nil {
		return decimal.Zero, false
	}
	tenantID := ex.ident.TenantID
	poolGroupID := ex.attempt.PoolGroupID
	if ex.groupRatioCacheSet && ex.groupRatioCacheTenantID == tenantID && ex.groupRatioCachePoolGroupID == poolGroupID {
		return ex.groupRatioCache, ex.groupRatioCachePendingReconciliation
	}
	if resolver, ok := ex.d.PricingRatioResolver.(pricingRatioResolverWithSignal); ok {
		ratio, pendingReconciliation, err := resolver.ResolveWithSignal(ex.ctx, tenantID, poolGroupID)
		if err != nil {
			return ex.cacheGroupPricingRatio(ex.defaultRatioAfterResolverError(err))
		}
		return ex.cacheGroupPricingRatio(ratio, pendingReconciliation)
	}
	ratio, err := ex.d.PricingRatioResolver.Resolve(ex.ctx, tenantID, poolGroupID)
	if err != nil {
		return ex.cacheGroupPricingRatio(ex.defaultRatioAfterResolverError(err))
	}
	return ex.cacheGroupPricingRatio(ratio, false)
}

func (ex *chatExecution) cacheGroupPricingRatio(ratio decimal.Decimal, pendingReconciliation bool) (decimal.Decimal, bool) {
	ex.groupRatioCacheSet = true
	ex.groupRatioCacheTenantID = ex.ident.TenantID
	ex.groupRatioCachePoolGroupID = ex.attempt.PoolGroupID
	ex.groupRatioCache = ratio
	ex.groupRatioCachePendingReconciliation = pendingReconciliation
	return ratio, pendingReconciliation
}

func (ex *chatExecution) defaultRatioAfterResolverError(err error) (decimal.Decimal, bool) {
	slog.ErrorContext(ex.ctx, "pricing ratio resolver error served default ratio",
		"tenant_id", ex.ident.TenantID,
		"pool_group_id", ex.attempt.PoolGroupID,
		"default_group_ratio", "1",
		"error", err,
	)
	return decimal.NewFromInt(1), true
}

func snapshotWithPricingRatioPending(snapshot string) string {
	const marker = "pending_reconciliation=pricing_ratio_backend_error"
	if strings.TrimSpace(snapshot) == "" {
		return marker
	}
	if strings.Contains(snapshot, "pending_reconciliation") {
		return snapshot
	}
	return snapshot + ";" + marker
}

func (ex *chatExecution) providerForPricing() string {
	for _, candidate := range []string{
		ex.cacheVendor,
		ex.accInfo.Platform,
		pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
	} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func (ex *chatExecution) modelCandidatesForPricing() []string {
	return compactStrings(
		ex.upstreamModelID,
		ex.resolved.ProviderModelID,
		ex.req.Model,
		ex.resolved.CanonicalModelID,
		"default",
		"*",
	)
}

func usageFromBufferedEnvelope(env *proto.HCSF) completionUsageForCost {
	usage := proto.CanonicalUsage{}
	if env != nil {
		usage = env.Accounting.Usage
		if env.BufferedResponse != nil {
			usage = env.BufferedResponse.Usage
		}
	}
	cacheCreationTokens := usage.CacheCreationInputTokens
	if cacheCreationTokens == 0 {
		cacheCreationTokens = usage.CacheCreationInputTokens5m + usage.CacheCreationInputTokens1h
	}
	return completionUsageForCost{
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CacheCreationTokens:   cacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreationInputTokens5m,
		CacheCreation1hTokens: usage.CacheCreationInputTokens1h,
		CacheReadTokens:       usage.CacheReadInputTokens,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch:       int64(usage.WebSearchCalls),
			FileSearch:      int64(usage.FileSearchCalls),
			ImageGeneration: int64(usage.ImageGenerationCalls),
		},
	}
}

func usageFromDraft(draft gateway.UsageRecordDraft) completionUsageForCost {
	return completionUsageForCost{
		InputTokens:           draft.TokensInput,
		OutputTokens:          draft.TokensOutput,
		CacheCreationTokens:   draft.CacheCreationTokens,
		CacheCreation5mTokens: draft.CacheCreation5mTokens,
		CacheCreation1hTokens: draft.CacheCreation1hTokens,
		CacheReadTokens:       draft.CacheReadTokens,
		ToolCallCounts: toolpricing.ToolCallCounts{
			WebSearch:       int64(draft.WebSearchCalls),
			FileSearch:      int64(draft.FileSearchCalls),
			ImageGeneration: int64(draft.ImageGenerationCalls),
		},
	}
}

func pricingUsage(usage completionUsageForCost) pricingeval.Usage {
	return pricingeval.Usage{
		InputTokens:           int64(usage.InputTokens),
		OutputTokens:          int64(usage.OutputTokens),
		CacheCreationTokens:   int64(usage.CacheCreationTokens),
		CacheCreation5mTokens: int64(usage.CacheCreation5mTokens),
		CacheCreation1hTokens: int64(usage.CacheCreation1hTokens),
		CacheReadTokens:       int64(usage.CacheReadTokens),
		ToolCallCounts:        usage.ToolCallCounts,
	}
}

func (v completionRateVector) flatRateFallback() pricingeval.FlatRateFallback {
	return pricingeval.FlatRateFallback{
		Input:              v.Input,
		Output:             v.Output,
		CacheCreation:      v.CacheCreation,
		CacheCreation5m:    v.CacheCreation5m,
		CacheCreation1h:    v.CacheCreation1h,
		CacheRead:          v.CacheRead,
		Multiplier:         v.Multiplier,
		HasInput:           v.HasInput,
		HasOutput:          v.HasOutput,
		HasCacheCreation:   v.HasCacheCreation,
		HasCacheCreation5m: v.HasCacheCreation5m,
		HasCacheCreation1h: v.HasCacheCreation1h,
		HasCacheRead:       v.HasCacheRead,
	}
}

func estimateInputTokens(body []byte) int {
	n := len(strings.TrimSpace(string(body)))
	if n <= 0 {
		return 1
	}
	return (n + 3) / 4
}

func estimateOutputTokens(req chatRequest) int {
	for _, v := range []*int{req.MaxCompletionTokens, req.MaxOutputTokens, req.MaxTokens} {
		if v != nil && *v > 0 {
			return *v
		}
	}
	return 1000
}

func rateVectorFromTable(raw json.RawMessage, provider string, models []string) (completionPricingSelection, error) {
	var root map[string]json.RawMessage
	if len(raw) == 0 {
		return completionPricingSelection{}, pricingUnavailable("rate table pricing_data empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return completionPricingSelection{}, pricingUnavailable(fmt.Sprintf("rate table pricing_data invalid: %v", err))
	}
	if len(root) == 0 {
		return completionPricingSelection{}, pricingUnavailable("rate table pricing_data has no models")
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseRateSelection(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseRateSelection(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(raw, models); ok {
		return parseRateSelection(rateRaw)
	}
	if looksLikeRateVector(root) {
		return parseRateSelection(raw)
	}
	return completionPricingSelection{}, pricingUnavailable(fmt.Sprintf("rate table missing model %q", firstNonEmptyPricing(models)))
}

func parseRateSelection(raw json.RawMessage) (completionPricingSelection, error) {
	rates, err := parseRateVector(raw)
	if err != nil {
		return completionPricingSelection{}, err
	}
	return completionPricingSelection{Raw: raw, Rates: rates}, nil
}

func modelRaw(raw json.RawMessage, models []string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	if modelsRaw, ok := rawField(obj, "models"); ok {
		return namedRaw(modelsRaw, models)
	}
	return namedRaw(raw, models)
}

func parseRateVector(raw json.RawMessage) (completionRateVector, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return completionRateVector{}, pricingUnavailable(fmt.Sprintf("rate vector invalid: %v", err))
	}
	out := completionRateVector{Multiplier: decimal.NewFromInt(1)}
	var err error
	out.Input, out.HasInput, err = decimalField(obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	out.Output, out.HasOutput, err = decimalField(obj, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd", "output_per_token_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	out.CacheCreation, out.HasCacheCreation, err = decimalField(obj, "cache_creation_micro_usd", "cache_write_rate_micro", "cache_creation_rate_micro", "cache_write_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	out.CacheCreation5m, out.HasCacheCreation5m, err = decimalField(obj, "cache_creation_5m_micro_usd", "cache_creation_ephemeral_5m_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	out.CacheCreation1h, out.HasCacheCreation1h, err = decimalField(obj, "cache_creation_1h_micro_usd", "cache_creation_ephemeral_1h_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	out.CacheRead, out.HasCacheRead, err = decimalField(obj, "cache_read_micro_usd", "cache_read_rate_micro", "cached_input_micro_usd")
	if err != nil {
		return completionRateVector{}, err
	}
	if multiplier, ok, err := decimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return completionRateVector{}, err
	} else if ok {
		out.Multiplier = multiplier
	}
	// Default the cache-read bucket to the input rate when a model is priced (has an
	// input rate) but omits an explicit cache-read rate. Cache-inclusive upstreams
	// (OpenAI / Gemini / OpenAI-compatible) report cached tokens for such models;
	// without this the additive model would fail closed (pricing_unavailable -> 503)
	// on every cache-hit response. Billing cached tokens at the input rate matches
	// new-api's cache-ratio default (1.0). Models with an explicit cache-read rate keep
	// it; truly unpriced models (no input rate) still fail closed.
	if out.HasInput && !out.HasCacheRead {
		out.CacheRead = out.Input
		out.HasCacheRead = true
	}
	return out, nil
}

func (v completionRateVector) price(usage completionUsageForCost) (completionCostBreakdown, error) {
	totalMicros := decimal.Zero
	var err error
	totalMicros, err = addTokenBucket(totalMicros, usage.InputTokens, v.Input, v.HasInput, "input")
	if err != nil {
		return completionCostBreakdown{}, err
	}
	totalMicros, err = addTokenBucket(totalMicros, usage.OutputTokens, v.Output, v.HasOutput, "output")
	if err != nil {
		return completionCostBreakdown{}, err
	}

	cacheCreationMicros := decimal.Zero
	if usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0 {
		var bucketMicros decimal.Decimal
		bucketMicros, err = cacheCreationTierMicros(
			usage.CacheCreation5mTokens,
			v.CacheCreation5m,
			v.HasCacheCreation5m,
			v.CacheCreation,
			v.HasCacheCreation,
			"cache_creation_5m",
		)
		if err != nil {
			return completionCostBreakdown{}, err
		}
		cacheCreationMicros = cacheCreationMicros.Add(bucketMicros)

		bucketMicros, err = cacheCreationTierMicros(
			usage.CacheCreation1hTokens,
			v.CacheCreation1h,
			v.HasCacheCreation1h,
			v.CacheCreation,
			v.HasCacheCreation,
			"cache_creation_1h",
		)
		if err != nil {
			return completionCostBreakdown{}, err
		}
		cacheCreationMicros = cacheCreationMicros.Add(bucketMicros)
	} else {
		cacheCreationMicros, err = tokenBucketMicros(usage.CacheCreationTokens, v.CacheCreation, v.HasCacheCreation, "cache_creation")
		if err != nil {
			return completionCostBreakdown{}, err
		}
	}
	totalMicros = totalMicros.Add(cacheCreationMicros)

	cacheReadMicros, err := tokenBucketMicros(usage.CacheReadTokens, v.CacheRead, v.HasCacheRead, "cache_read")
	if err != nil {
		return completionCostBreakdown{}, err
	}
	totalMicros = totalMicros.Add(cacheReadMicros)

	if v.Multiplier.IsNegative() || v.Multiplier.IsZero() {
		return completionCostBreakdown{}, pricingUnavailable("rate vector model_multiplier must be positive")
	}
	return completionCostBreakdown{
		Total:             scaledMicros(totalMicros, v.Multiplier),
		CacheCreationCost: scaledMicros(cacheCreationMicros, v.Multiplier),
		CacheReadCost:     scaledMicros(cacheReadMicros, v.Multiplier),
	}, nil
}

func cacheCreationTierMicros(tokens int, tierRate decimal.Decimal, hasTierRate bool, fallbackRate decimal.Decimal, hasFallbackRate bool, bucket string) (decimal.Decimal, error) {
	if tokens <= 0 {
		return decimal.Zero, nil
	}
	if hasTierRate {
		return tokenBucketMicros(tokens, tierRate, true, bucket)
	}
	return tokenBucketMicros(tokens, fallbackRate, hasFallbackRate, bucket)
}

func scaledMicros(micros decimal.Decimal, multiplier decimal.Decimal) decimal.Decimal {
	return micros.Mul(multiplier).Div(decimal.NewFromInt(1_000_000))
}

func addTokenBucket(total decimal.Decimal, tokens int, rate decimal.Decimal, hasRate bool, bucket string) (decimal.Decimal, error) {
	micros, err := tokenBucketMicros(tokens, rate, hasRate, bucket)
	if err != nil {
		return decimal.Zero, err
	}
	return total.Add(micros), nil
}

func tokenBucketMicros(tokens int, rate decimal.Decimal, hasRate bool, bucket string) (decimal.Decimal, error) {
	if tokens <= 0 {
		return decimal.Zero, nil
	}
	if !hasRate {
		return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s rate missing", bucket))
	}
	if rate.IsNegative() {
		return decimal.Zero, pricingUnavailable(fmt.Sprintf("%s rate negative", bucket))
	}
	return decimal.NewFromInt(int64(tokens)).Mul(rate), nil
}

func decimalField(obj map[string]json.RawMessage, keys ...string) (decimal.Decimal, bool, error) {
	for _, key := range keys {
		raw, ok := rawField(obj, key)
		if !ok {
			continue
		}
		value, err := parseDecimalRaw(raw)
		if err != nil {
			return decimal.Zero, false, pricingUnavailable(fmt.Sprintf("%s invalid: %v", key, err))
		}
		return value, true, nil
	}
	return decimal.Zero, false, nil
}

func parseDecimalRaw(raw json.RawMessage) (decimal.Decimal, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return decimal.NewFromString(strings.TrimSpace(s))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}

func rawField(obj map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := obj[key]; ok {
		return raw, true
	}
	normalizedKey := normalizePricingKey(key)
	for k, raw := range obj {
		if normalizePricingKey(k) == normalizedKey {
			return raw, true
		}
	}
	return nil, false
}

func namedRaw(raw json.RawMessage, names []string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if value, ok := obj[name]; ok {
			return value, true
		}
		normalizedName := normalizePricingKey(name)
		for key, value := range obj {
			if normalizePricingKey(key) == normalizedName {
				return value, true
			}
		}
	}
	return nil, false
}

func looksLikeRateVector(obj map[string]json.RawMessage) bool {
	for _, key := range []string{
		"input_micro_usd",
		"input_rate_micro",
		"output_micro_usd",
		"output_rate_micro",
		"cache_creation_micro_usd",
		"cache_creation_5m_micro_usd",
		"cache_creation_1h_micro_usd",
		"cache_read_micro_usd",
	} {
		if _, ok := rawField(obj, key); ok {
			return true
		}
	}
	return false
}

func normalizePricingKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := normalizePricingKey(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmptyPricing(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pricingUnavailable(reason string) error {
	return fmt.Errorf("%w: %s", errCompletionPricingUnavailable, reason)
}
