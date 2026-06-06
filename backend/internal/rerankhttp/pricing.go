package rerankhttp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

func (ex *execution) searchUnitCost(units int) (decimal.Decimal, string, bool, error) {
	if units <= 0 {
		return decimal.Zero, "", false, fmt.Errorf("search units missing")
	}
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		return decimal.Zero, "", false, fmt.Errorf("billing policy version empty")
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		return decimal.Zero, "", false, err
	}
	selection, err := searchUnitRateFromTable(table.PricingData, ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return decimal.Zero, "", false, err
	}
	fallback := selection.fallback()
	groupRatio, err := ex.groupPricingRatio()
	if err != nil {
		return decimal.Zero, "", false, err
	}
	fallback.GroupRatio = groupRatio
	result, err := pricingeval.Resolve(ex.ctx, selection.raw, pricingeval.Usage{
		BillableUnits: decimal.NewFromInt(int64(units)),
	}, fallback, version)
	if err != nil {
		return decimal.Zero, "", false, err
	}
	return result.Total, result.CostSnapshot, result.PendingReconciliation, nil
}

func (ex *execution) groupPricingRatio() (decimal.Decimal, error) {
	if ex == nil || ex.d.PricingRatioResolver == nil {
		return decimal.Zero, nil
	}
	return ex.d.PricingRatioResolver.Resolve(ex.ctx, ex.ident.TenantID, ex.attempt.PoolGroupID)
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

type searchUnitRateSelection struct {
	raw        json.RawMessage
	perUnit    decimal.Decimal
	multiplier decimal.Decimal
	hasPerUnit bool
}

func (s searchUnitRateSelection) fallback() pricingeval.FlatRateFallback {
	return pricingeval.FlatRateFallback{PerUnit: s.perUnit, Multiplier: s.multiplier, HasPerUnit: s.hasPerUnit}
}

func searchUnitRateFromTable(raw json.RawMessage, provider string, models []string) (searchUnitRateSelection, error) {
	root := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return searchUnitRateSelection{}, fmt.Errorf("rate table empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return searchUnitRateSelection{}, err
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseSearchUnitRate(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseSearchUnitRate(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(raw, models); ok {
		return parseSearchUnitRate(rateRaw)
	}
	for _, key := range searchUnitRateKeys() {
		if _, ok := rawField(root, key); ok {
			return parseSearchUnitRate(raw)
		}
	}
	return searchUnitRateSelection{}, fmt.Errorf("rate table missing rerank model")
}

func parseSearchUnitRate(raw json.RawMessage) (searchUnitRateSelection, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return searchUnitRateSelection{}, err
	}
	perUnit, hasPerUnit, err := decimalField(obj, searchUnitRateKeys()...)
	if err != nil {
		return searchUnitRateSelection{}, err
	}
	multiplier := decimal.NewFromInt(1)
	if value, ok, err := decimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return searchUnitRateSelection{}, err
	} else if ok {
		multiplier = value
	}
	return searchUnitRateSelection{raw: raw, perUnit: perUnit, multiplier: multiplier, hasPerUnit: hasPerUnit}, nil
}

func searchUnitRateKeys() []string {
	return []string{
		"search_unit_micro_usd",
		"search_units_micro_usd",
		"rerank_search_unit_micro_usd",
		"request_micro_usd",
		"per_unit_micro_usd",
		"unit_micro_usd",
		"flat_micro_usd",
	}
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
