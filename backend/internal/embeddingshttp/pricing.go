package embeddingshttp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

func (ex *execution) inputCost(tokens int) (decimal.Decimal, string, error) {
	if tokens <= 0 {
		return decimal.Zero, "", fmt.Errorf("input tokens missing")
	}
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		return decimal.Zero, "", fmt.Errorf("billing policy version empty")
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		return decimal.Zero, "", err
	}
	selection, err := inputRateFromTable(table.PricingData, ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return decimal.Zero, "", err
	}
	result, err := pricingeval.Resolve(ex.ctx, selection.raw, pricingeval.Usage{InputTokens: int64(tokens)}, selection.fallback(), version)
	if err != nil {
		return decimal.Zero, "", err
	}
	return result.Total, result.CostSnapshot, nil
}

func (ex *execution) providerForPricing() string {
	for _, candidate := range []string{ex.accInfo.Platform, pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily)} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func (ex *execution) modelCandidatesForPricing() []string {
	return compactStrings(ex.upstreamModelID, ex.resolved.ProviderModelID, ex.req.Model, ex.resolved.CanonicalModelID, "default", "*")
}

type inputRateSelection struct {
	raw        json.RawMessage
	input      decimal.Decimal
	multiplier decimal.Decimal
	hasInput   bool
}

func (s inputRateSelection) fallback() pricingeval.FlatRateFallback {
	return pricingeval.FlatRateFallback{Input: s.input, Multiplier: s.multiplier, HasInput: s.hasInput}
}

func inputRateFromTable(raw json.RawMessage, provider string, models []string) (inputRateSelection, error) {
	root := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return inputRateSelection{}, fmt.Errorf("rate table empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return inputRateSelection{}, err
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseInputRate(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseInputRate(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(raw, models); ok {
		return parseInputRate(rateRaw)
	}
	if _, ok := rawField(root, "input_micro_usd"); ok {
		return parseInputRate(raw)
	}
	return inputRateSelection{}, fmt.Errorf("rate table missing embeddings model")
}

func parseInputRate(raw json.RawMessage) (inputRateSelection, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return inputRateSelection{}, err
	}
	input, hasInput, err := decimalField(obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return inputRateSelection{}, err
	}
	multiplier := decimal.NewFromInt(1)
	if value, ok, err := decimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return inputRateSelection{}, err
	} else if ok {
		multiplier = value
	}
	return inputRateSelection{raw: raw, input: input, multiplier: multiplier, hasInput: hasInput}, nil
}

func rawField(obj map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := obj[key]; ok {
		return raw, true
	}
	normalized := normalizeKey(key)
	for k, raw := range obj {
		if normalizeKey(k) == normalized {
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
		normalized := normalizeKey(name)
		for key, value := range obj {
			if normalizeKey(key) == normalized {
				return value, true
			}
		}
	}
	return nil, false
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

func decimalField(obj map[string]json.RawMessage, keys ...string) (decimal.Decimal, bool, error) {
	for _, key := range keys {
		raw, ok := rawField(obj, key)
		if !ok {
			continue
		}
		value, err := parseDecimal(raw)
		if err != nil {
			return decimal.Zero, false, err
		}
		return value, true, nil
	}
	return decimal.Zero, false, nil
}

func parseDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return decimal.NewFromString(strings.TrimSpace(s))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := normalizeKey(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}
