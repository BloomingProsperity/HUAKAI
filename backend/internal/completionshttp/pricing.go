package completionshttp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

type completionCostBreakdown struct {
	Total                 decimal.Decimal
	CostSnapshot          string
	PendingReconciliation bool
}

func (ex *execution) inputCost(tokens int) (completionCostBreakdown, error) {
	return ex.completionCost(completionUsage{PromptTokens: tokens})
}

func (ex *execution) actualCost(usage completionUsage) (completionCostBreakdown, error) {
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		return completionCostBreakdown{}, fmt.Errorf("reported usage missing")
	}
	return ex.completionCost(usage)
}

func (ex *execution) completionCost(usage completionUsage) (completionCostBreakdown, error) {
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		return completionCostBreakdown{}, fmt.Errorf("billing policy version empty")
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		return completionCostBreakdown{}, err
	}
	selection, err := completionRateFromTable(table.PricingData, ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return completionCostBreakdown{}, err
	}
	fallback := selection.fallback()
	groupRatio, err := ex.groupPricingRatio()
	if err != nil {
		return completionCostBreakdown{}, err
	}
	fallback.GroupRatio = groupRatio
	result, err := pricingeval.Resolve(ex.ctx, selection.raw, pricingeval.Usage{
		InputTokens:  int64(usage.PromptTokens),
		OutputTokens: int64(usage.CompletionTokens),
	}, fallback, version)
	if err != nil {
		return completionCostBreakdown{}, err
	}
	return completionCostBreakdown{
		Total:                 result.Total,
		CostSnapshot:          result.CostSnapshot,
		PendingReconciliation: result.PendingReconciliation,
	}, nil
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

type completionRateSelection struct {
	raw        json.RawMessage
	input      decimal.Decimal
	output     decimal.Decimal
	multiplier decimal.Decimal
	hasInput   bool
	hasOutput  bool
}

func (s completionRateSelection) fallback() pricingeval.FlatRateFallback {
	return pricingeval.FlatRateFallback{
		Input:      s.input,
		Output:     s.output,
		Multiplier: s.multiplier,
		HasInput:   s.hasInput,
		HasOutput:  s.hasOutput,
	}
}

func completionRateFromTable(raw json.RawMessage, provider string, models []string) (completionRateSelection, error) {
	root := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return completionRateSelection{}, fmt.Errorf("rate table empty")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return completionRateSelection{}, err
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseCompletionRate(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseCompletionRate(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(raw, models); ok {
		return parseCompletionRate(rateRaw)
	}
	if _, ok := rawField(root, "input_micro_usd"); ok {
		return parseCompletionRate(raw)
	}
	return completionRateSelection{}, fmt.Errorf("rate table missing completions model")
}

func parseCompletionRate(raw json.RawMessage) (completionRateSelection, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return completionRateSelection{}, err
	}
	input, hasInput, err := decimalField(obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return completionRateSelection{}, err
	}
	output, hasOutput, err := decimalField(obj, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd", "output_per_token_micro_usd")
	if err != nil {
		return completionRateSelection{}, err
	}
	multiplier := decimal.NewFromInt(1)
	if value, ok, err := decimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return completionRateSelection{}, err
	} else if ok {
		multiplier = value
	}
	return completionRateSelection{raw: raw, input: input, output: output, multiplier: multiplier, hasInput: hasInput, hasOutput: hasOutput}, nil
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
