package audiohttp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/audiopricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

type audioTokenUsage struct {
	InputTokens  int
	OutputTokens int
}

func (ex *execution) preparePricing() error {
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		return fmt.Errorf("billing policy version empty")
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		return err
	}
	catalog, err := audiopricing.NewCatalog(table.PricingData)
	if err != nil {
		return err
	}
	ex.catalog = catalog
	scheme, err := catalog.SchemeFor(ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return err
	}
	ex.scheme = scheme
	switch ex.endpoint {
	case audioEndpointSpeech:
		if scheme != audiopricing.SchemePerChar {
			return fmt.Errorf("audio speech requires per_char pricing, got %s", scheme)
		}
		perUnit, err := catalog.CharMicroUSD(ex.providerForPricing(), ex.modelCandidatesForPricing())
		if err != nil {
			return err
		}
		ex.predictedCost, ex.costSnapshot, ex.pending, err = ex.perUnitCost(decimal.NewFromInt(int64(ex.charCount)), perUnit)
		return err
	default:
		switch scheme {
		case audiopricing.SchemePerSecond:
			perUnit, err := catalog.SecondMicroUSD(ex.providerForPricing(), ex.modelCandidatesForPricing())
			if err != nil {
				return err
			}
			ex.predictedCost, ex.costSnapshot, ex.pending, err = ex.perUnitCost(ex.estimatedDuration.Seconds, perUnit)
			return err
		case audiopricing.SchemeToken:
			usage := ex.reserveTokenUsage()
			ex.predictedCost, ex.costSnapshot, ex.pending, err = ex.tokenCost(usage)
			return err
		default:
			return fmt.Errorf("unsupported audio pricing scheme %s", scheme)
		}
	}
}

func (ex *execution) perUnitCost(units, perUnit decimal.Decimal) (decimal.Decimal, string, bool, error) {
	result, err := pricingeval.Resolve(ex.ctx, json.RawMessage(`{}`), pricingeval.Usage{
		BillableUnits: units,
	}, pricingeval.FlatRateFallback{
		PerUnit:    perUnit,
		Multiplier: decimal.NewFromInt(1),
		GroupRatio: ex.groupPricingRatio(),
		HasPerUnit: true,
	}, strings.TrimSpace(ex.d.BillingPolicyVersion))
	if err != nil {
		return decimal.Zero, "", false, err
	}
	return result.Total, result.CostSnapshot, result.PendingReconciliation, nil
}

func (ex *execution) tokenCost(usage audioTokenUsage) (decimal.Decimal, string, bool, error) {
	rates, err := ex.catalog.TokenRates(ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return decimal.Zero, "", false, err
	}
	result, err := pricingeval.Resolve(ex.ctx, rates.Raw, pricingeval.Usage{
		InputTokens:  int64(usage.InputTokens),
		OutputTokens: int64(usage.OutputTokens),
	}, pricingeval.FlatRateFallback{
		Input:      rates.Input,
		Output:     rates.Output,
		Multiplier: rates.Multiplier,
		GroupRatio: ex.groupPricingRatio(),
		HasInput:   rates.HasInput,
		HasOutput:  rates.HasOutput,
	}, strings.TrimSpace(ex.d.BillingPolicyVersion))
	if err != nil {
		return decimal.Zero, "", false, err
	}
	return result.Total, result.CostSnapshot, result.PendingReconciliation, nil
}

func (ex *execution) reserveTokenUsage() audioTokenUsage {
	if len(ex.req.File.Data) > 0 {
		return audioTokenUsage{InputTokens: len(ex.req.File.Data)}
	}
	if ex.charCount > 0 {
		return audioTokenUsage{InputTokens: ex.charCount}
	}
	return audioTokenUsage{InputTokens: 1}
}

func (ex *execution) groupPricingRatio() decimal.Decimal {
	if ex == nil || ex.d.PricingRatioResolver == nil {
		return decimal.Zero
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
