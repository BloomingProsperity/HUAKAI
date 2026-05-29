package gatewayhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

var errCompletionPricingUnavailable = errors.New("gatewayhttp: pricing unavailable")

type completionUsageForCost struct {
	InputTokens           int
	OutputTokens          int
	CacheCreationTokens   int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	CacheReadTokens       int
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
	Total             decimal.Decimal
	CacheCreationCost decimal.Decimal
	CacheReadCost     decimal.Decimal
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

func (ex *chatExecution) completionCost(usage completionUsageForCost) (completionCostBreakdown, error) {
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
	rates, err := rateVectorFromTable(table.PricingData, ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return completionCostBreakdown{}, err
	}
	return rates.price(usage)
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

func rateVectorFromTable(raw json.RawMessage, provider string, models []string) (completionRateVector, error) {
	var root map[string]json.RawMessage
	if len(raw) == 0 {
		return completionRateVector{}, pricingUnavailable("rate table pricing_data empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return completionRateVector{}, pricingUnavailable(fmt.Sprintf("rate table pricing_data invalid: %v", err))
	}
	if len(root) == 0 {
		return completionRateVector{}, pricingUnavailable("rate table pricing_data has no models")
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseRateVector(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseRateVector(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(raw, models); ok {
		return parseRateVector(rateRaw)
	}
	if looksLikeRateVector(root) {
		return parseRateVector(raw)
	}
	return completionRateVector{}, pricingUnavailable(fmt.Sprintf("rate table missing model %q", firstNonEmptyPricing(models)))
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
