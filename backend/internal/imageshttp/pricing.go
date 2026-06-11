package imageshttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

func (ex *execution) preparePricing(w http.ResponseWriter) bool {
	version := strings.TrimSpace(ex.d.BillingPolicyVersion)
	if version == "" {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	table, err := ex.d.RateTables.GetRateTable(ex.ctx, version)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	catalog, err := imagepricing.NewCatalog(table.PricingData)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	ex.catalog = catalog
	if !ex.validateCatalogRequest(w) {
		return false
	}
	switch ex.scheme {
	case imagepricing.SchemePerImage:
		ex.predictedCost, ex.costSnapshot, ex.pending, err = ex.perImageCost(ex.amount)
	case imagepricing.SchemeTokenImage:
		ex.predictedCost, ex.costSnapshot, ex.pending, err = ex.tokenImageEstimate()
	default:
		err = fmt.Errorf("unsupported image scheme %q", ex.scheme)
	}
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	return true
}

func (ex *execution) validateCatalogRequest(w http.ResponseWriter) bool {
	provider := ex.providerForPricing()
	models := ex.modelCandidatesForPricing()
	scheme, err := ex.catalog.SchemeFor(provider, models)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	ex.scheme = scheme
	sizes, err := ex.catalog.AllowedSizes(provider, models)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	ex.size = strings.TrimSpace(ex.req.Size)
	if ex.size == "" {
		ex.size = defaultSize(sizes)
	}
	if !slices.Contains(sizes, ex.size) {
		writeJSONError(w, http.StatusBadRequest, "invalid_size", "size not allowed for model")
		return false
	}
	rng, err := ex.catalog.AmountRange(provider, models)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	if ex.amount < rng.Min || ex.amount > rng.Max {
		writeJSONError(w, http.StatusBadRequest, "invalid_n", "n outside model amount range")
		return false
	}
	maxChars, err := ex.catalog.PromptMaxChars(provider, models)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return false
	}
	if maxChars > 0 && promptCharCount(ex.req.PromptText()) > maxChars {
		writeJSONError(w, http.StatusBadRequest, "prompt_too_long", "prompt exceeds model maximum")
		return false
	}
	return true
}

func (ex *execution) perImageCost(n int) (decimal.Decimal, string, bool, error) {
	perUnit, err := ex.catalog.PerImageMicroUSD(ex.providerForPricing(), ex.modelCandidatesForPricing(), ex.size, ex.quality)
	if err != nil {
		return decimal.Zero, "", false, err
	}
	groupRatio, err := ex.groupPricingRatio()
	if err != nil {
		return decimal.Zero, "", false, err
	}
	result, err := pricingeval.Resolve(ex.ctx, json.RawMessage(`{}`), pricingeval.Usage{
		BillableUnits: decimal.NewFromInt(int64(n)),
	}, pricingeval.FlatRateFallback{
		PerUnit:    perUnit,
		Multiplier: decimal.NewFromInt(1),
		GroupRatio: groupRatio,
		HasPerUnit: true,
	}, strings.TrimSpace(ex.d.BillingPolicyVersion))
	if err != nil {
		return decimal.Zero, "", false, err
	}
	return result.Total, result.CostSnapshot, result.PendingReconciliation, nil
}

func (ex *execution) tokenImageEstimate() (decimal.Decimal, string, bool, error) {
	outputBound, err := ex.catalog.OutputTokenUpperBound(ex.providerForPricing(), ex.modelCandidatesForPricing(), ex.size)
	if err != nil {
		return decimal.Zero, "", false, err
	}
	inputEstimate := estimatePromptTokens(ex.req.PromptText())
	return ex.tokenImageCost(tokenImageUsage{
		InputTokens:  inputEstimate,
		OutputTokens: outputBound * ex.amount,
	})
}

func (ex *execution) tokenImageCost(usage tokenImageUsage) (decimal.Decimal, string, bool, error) {
	rates, err := ex.catalog.TokenRates(ex.providerForPricing(), ex.modelCandidatesForPricing())
	if err != nil {
		return decimal.Zero, "", false, err
	}
	groupRatio, err := ex.groupPricingRatio()
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
		GroupRatio: groupRatio,
		HasInput:   rates.HasInput,
		HasOutput:  rates.HasOutput,
	}, strings.TrimSpace(ex.d.BillingPolicyVersion))
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
	for _, candidate := range []string{ex.accInfo.Platform, pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily), pricingVendorForFamily(ex.resolved.ProtocolFamily)} {
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

func defaultSize(sizes []string) string {
	if slices.Contains(sizes, "1024x1024") {
		return "1024x1024"
	}
	if len(sizes) > 0 {
		return sizes[0]
	}
	return ""
}
