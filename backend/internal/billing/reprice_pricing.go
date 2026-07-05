package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

var ErrRepricePricingUnavailable = errors.New("billing: reprice pricing unavailable")

type repriceRateVector struct {
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

type repricePricingSelection struct {
	Raw    json.RawMessage
	Rates  repriceRateVector
	Source string
}

func repriceCostFromCurrentPricing(ctx context.Context, table RateTable, row repriceUsageRecordRow, groupRatio decimal.Decimal) (decimal.Decimal, string, error) {
	usage := row.pricingUsage()
	selection, err := repriceRateFromTable(table.PricingData, row.providerForPricing(), row.modelCandidatesForPricing())
	if err != nil {
		return decimal.Zero, "", err
	}
	fallback := selection.Rates.flatRateFallback()
	fallback.GroupRatio = groupRatio
	result, err := pricingeval.Resolve(ctx, selection.Raw, usage, fallback, table.Version)
	if err != nil {
		return decimal.Zero, "", repricePricingUnavailable(err.Error())
	}
	if result.PendingReconciliation {
		return decimal.Zero, "", repricePricingUnavailable("current pricing still requires reconciliation")
	}
	source := repricePricingSource(table, row, selection, result.CostSnapshot, groupRatio)
	return normalizeRepriceMoney(result.Total), source, nil
}

func (r repriceUsageRecordRow) pricingUsage() pricingeval.Usage {
	inputTokens := int64(r.TokensInput)
	if !inputTokensExcludeCacheForReprice(r.ProtocolFamily) {
		inputTokens -= int64(r.CacheReadTokens + r.CacheCreationTokens)
		if inputTokens < 0 {
			inputTokens = 0
		}
	}
	return pricingeval.Usage{
		InputTokens:           inputTokens,
		OutputTokens:          int64(r.TokensOutput),
		CacheCreationTokens:   int64(r.CacheCreationTokens),
		CacheCreation5mTokens: int64(r.CacheCreation5mTokens),
		CacheCreation1hTokens: int64(r.CacheCreation1hTokens),
		CacheReadTokens:       int64(r.CacheReadTokens),
	}
}

func inputTokensExcludeCacheForReprice(protocolFamily string) bool {
	switch strings.TrimSpace(protocolFamily) {
	case "anthropic_messages", "bedrock_invoke":
		return true
	default:
		return false
	}
}

func (r repriceUsageRecordRow) providerForPricing() string {
	for _, candidate := range []string{
		r.ProviderCode,
		pool.VendorFromProtocolFamily(r.ProtocolFamily),
	} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func (r repriceUsageRecordRow) modelCandidatesForPricing() []string {
	return repriceCompactStrings(r.UpstreamModel, r.RequestedModel, r.ClaimRequestedModel, "default", "*")
}

func (v repriceRateVector) flatRateFallback() pricingeval.FlatRateFallback {
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

func repriceRateFromTable(raw json.RawMessage, provider string, models []string) (repricePricingSelection, error) {
	var root map[string]json.RawMessage
	if len(raw) == 0 {
		return repricePricingSelection{}, repricePricingUnavailable("rate table pricing_data empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return repricePricingSelection{}, repricePricingUnavailable(fmt.Sprintf("rate table pricing_data invalid: %v", err))
	}
	if len(root) == 0 {
		return repricePricingSelection{}, repricePricingUnavailable("rate table pricing_data has no models")
	}
	if providersRaw, ok := repriceRawField(root, "providers"); ok {
		if providerRaw, matchedProvider, ok := repriceNamedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, matchedModel, ok := repriceModelRaw(providerRaw, models); ok {
				return repriceParseRateSelection(rateRaw, "providers."+matchedProvider+".models."+matchedModel)
			}
		}
	}
	if modelsRaw, ok := repriceRawField(root, "models"); ok {
		if rateRaw, matchedModel, ok := repriceNamedRaw(modelsRaw, models); ok {
			return repriceParseRateSelection(rateRaw, "models."+matchedModel)
		}
	}
	if rateRaw, matchedModel, ok := repriceNamedRaw(raw, models); ok {
		return repriceParseRateSelection(rateRaw, "models."+matchedModel)
	}
	if repriceLooksLikeRateVector(root) {
		return repriceParseRateSelection(raw, "root")
	}
	return repricePricingSelection{}, repricePricingUnavailable(fmt.Sprintf("rate table missing model %q", repriceFirstNonEmpty(models)))
}

func repriceParseRateSelection(raw json.RawMessage, source string) (repricePricingSelection, error) {
	rates, err := repriceParseRateVector(raw)
	if err != nil {
		return repricePricingSelection{}, err
	}
	return repricePricingSelection{Raw: raw, Rates: rates, Source: source}, nil
}

func repriceModelRaw(raw json.RawMessage, models []string) (json.RawMessage, string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, "", false
	}
	if modelsRaw, ok := repriceRawField(obj, "models"); ok {
		return repriceNamedRaw(modelsRaw, models)
	}
	return repriceNamedRaw(raw, models)
}

func repriceParseRateVector(raw json.RawMessage) (repriceRateVector, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return repriceRateVector{}, repricePricingUnavailable(fmt.Sprintf("rate vector invalid: %v", err))
	}
	out := repriceRateVector{Multiplier: decimal.NewFromInt(1)}
	var err error
	out.Input, out.HasInput, err = repriceDecimalField(obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	out.Output, out.HasOutput, err = repriceDecimalField(obj, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd", "output_per_token_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	out.CacheCreation, out.HasCacheCreation, err = repriceDecimalField(obj, "cache_creation_micro_usd", "cache_write_rate_micro", "cache_creation_rate_micro", "cache_write_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	out.CacheCreation5m, out.HasCacheCreation5m, err = repriceDecimalField(obj, "cache_creation_5m_micro_usd", "cache_creation_ephemeral_5m_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	out.CacheCreation1h, out.HasCacheCreation1h, err = repriceDecimalField(obj, "cache_creation_1h_micro_usd", "cache_creation_ephemeral_1h_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	out.CacheRead, out.HasCacheRead, err = repriceDecimalField(obj, "cache_read_micro_usd", "cache_read_rate_micro", "cached_input_micro_usd")
	if err != nil {
		return repriceRateVector{}, err
	}
	if multiplier, ok, err := repriceDecimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return repriceRateVector{}, err
	} else if ok {
		out.Multiplier = multiplier
	}
	if out.HasInput && !out.HasCacheRead {
		out.CacheRead = out.Input
		out.HasCacheRead = true
	}
	return out, nil
}

func repriceDecimalField(obj map[string]json.RawMessage, keys ...string) (decimal.Decimal, bool, error) {
	for _, key := range keys {
		raw, ok := repriceRawField(obj, key)
		if !ok {
			continue
		}
		value, err := repriceParseDecimalRaw(raw)
		if err != nil {
			return decimal.Zero, false, repricePricingUnavailable(fmt.Sprintf("%s invalid: %v", key, err))
		}
		return value, true, nil
	}
	return decimal.Zero, false, nil
}

func repriceParseDecimalRaw(raw json.RawMessage) (decimal.Decimal, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return decimal.NewFromString(strings.TrimSpace(s))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}

func repriceRawField(obj map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := obj[key]; ok {
		return raw, true
	}
	normalizedKey := repriceNormalizeKey(key)
	for k, raw := range obj {
		if repriceNormalizeKey(k) == normalizedKey {
			return raw, true
		}
	}
	return nil, false
}

func repriceNamedRaw(raw json.RawMessage, names []string) (json.RawMessage, string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, "", false
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if value, ok := obj[name]; ok {
			return value, name, true
		}
		normalizedName := repriceNormalizeKey(name)
		for key, value := range obj {
			if repriceNormalizeKey(key) == normalizedName {
				return value, key, true
			}
		}
	}
	return nil, "", false
}

func repriceLooksLikeRateVector(obj map[string]json.RawMessage) bool {
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
		if _, ok := repriceRawField(obj, key); ok {
			return true
		}
	}
	return false
}

func repriceNormalizeKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}

func repriceCompactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := repriceNormalizeKey(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func repriceFirstNonEmpty(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func repricePricingSource(table RateTable, row repriceUsageRecordRow, selection repricePricingSelection, snapshot string, ratio decimal.Decimal) string {
	parts := []string{
		"billing_policy_version=" + strings.TrimSpace(table.Version),
		fmt.Sprintf("rate_table_id=%d", table.ID),
		"rate_source=" + strings.TrimSpace(selection.Source),
		"cost_snapshot=" + strings.TrimSpace(snapshot),
		"group_ratio=" + ratio.String(),
	}
	if provider := strings.TrimSpace(row.providerForPricing()); provider != "" {
		parts = append(parts, "provider="+provider)
	}
	if protocol := strings.TrimSpace(row.ProtocolFamily); protocol != "" {
		parts = append(parts, "protocol_family="+protocol)
	}
	return strings.Join(parts, ";")
}

func repricePricingUnavailable(reason string) error {
	return fmt.Errorf("%w: %s", ErrRepricePricingUnavailable, reason)
}
