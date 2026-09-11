package modelrate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type catalogKey struct {
	Vendor string
	Model  string
}

// BuildCatalog 从官方价表 JSON 与覆盖列表生成对照目录。
// 供应商价表中的模型归到对应厂商;仅出现在顶层 models 的条目按模型名前缀归组。
func BuildCatalog(official json.RawMessage, overrides []Override, vendorFilter string) ([]CatalogItem, error) {
	officialByKey, err := parseOfficialCatalog(official)
	if err != nil {
		return nil, err
	}
	items := make(map[catalogKey]CatalogItem, len(officialByKey)+len(overrides))
	for key, officialRates := range officialByKey {
		items[key] = CatalogItem{
			Vendor:    key.Vendor,
			Model:     key.Model,
			Official:  officialRates,
			Effective: officialRates.Clone(),
			Source:    SourceOfficial,
		}
	}
	for _, overlay := range overrides {
		key := catalogKey{Vendor: normalizeVendor(overlay.Vendor), Model: normalizeModel(overlay.Model)}
		if key.Vendor == "" || key.Model == "" {
			return nil, fmt.Errorf("%w: override missing vendor or model", ErrInvalidInput)
		}
		item, ok := items[key]
		if !ok {
			item = CatalogItem{Vendor: key.Vendor, Model: key.Model, Source: SourceOfficial}
		}
		item.Effective = applyBuckets(item.Official, overlay.Rates)
		item.Overridden = true
		item.Source = SourceOverride
		items[key] = item
	}

	filter := normalizeVendor(vendorFilter)
	out := make([]CatalogItem, 0, len(items))
	for _, item := range items {
		if filter != "" && item.Vendor != filter {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Vendor != out[j].Vendor {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func FindCatalogItem(items []CatalogItem, vendor, model string) (CatalogItem, bool) {
	vendor = normalizeVendor(vendor)
	model = normalizeModel(model)
	for _, item := range items {
		if item.Vendor == vendor && item.Model == model {
			return item, true
		}
	}
	return CatalogItem{}, false
}

func parseOfficialCatalog(official json.RawMessage) (map[catalogKey]RateBuckets, error) {
	root, err := decodeObject(official)
	if err != nil {
		return nil, fmt.Errorf("%w: official pricing_data: %v", ErrInvalidInput, err)
	}
	out := map[catalogKey]RateBuckets{}
	seenModels := map[string]struct{}{}

	providers, err := nestedObject(root, "providers")
	if err != nil {
		return nil, err
	}
	for vendor, raw := range providers {
		vendor = normalizeVendor(vendor)
		if vendor == "" {
			continue
		}
		vendorObj, err := decodeObject(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s: %v", ErrInvalidInput, vendor, err)
		}
		models, err := nestedObject(vendorObj, "models")
		if err != nil {
			return nil, err
		}
		for model, rateRaw := range models {
			model = normalizeModel(model)
			if model == "" {
				continue
			}
			rates, err := parseRateBuckets(rateRaw)
			if err != nil {
				return nil, err
			}
			if !rates.HasAny() {
				continue
			}
			out[catalogKey{Vendor: vendor, Model: model}] = rates
			seenModels[strings.ToLower(model)] = struct{}{}
		}
	}

	models, err := nestedObject(root, "models")
	if err != nil {
		return nil, err
	}
	for model, rateRaw := range models {
		model = normalizeModel(model)
		if model == "" {
			continue
		}
		if _, exists := seenModels[strings.ToLower(model)]; exists {
			continue
		}
		rates, err := parseRateBuckets(rateRaw)
		if err != nil {
			return nil, err
		}
		if !rates.HasAny() {
			continue
		}
		out[catalogKey{Vendor: InferVendor(model), Model: model}] = rates
	}
	return out, nil
}

// InferVendor 只用于把顶层官方模型归到厂商浏览组,不决定覆盖主键。
func InferVendor(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	switch {
	case hasModelPrefix(name, "gpt-", "chatgpt-", "o1", "o3", "o4"):
		return "openai"
	case hasModelPrefix(name, "claude-"):
		return "anthropic"
	case hasModelPrefix(name, "gemini-"):
		return "google"
	case hasModelPrefix(name, "grok-"):
		return "xai"
	case hasModelPrefix(name, "deepseek-"):
		return "deepseek"
	case hasModelPrefix(name, "command-"):
		return "cohere"
	case hasModelPrefix(name, "moonshot-"):
		return "moonshot"
	case hasModelPrefix(name, "together-"):
		return "together"
	default:
		return "catalog"
	}
}

func hasModelPrefix(name string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if name == prefix || strings.HasPrefix(name, prefix) || strings.HasPrefix(name, prefix+"-") {
			if prefix == "o1" || prefix == "o3" || prefix == "o4" {
				if name == prefix || strings.HasPrefix(name, prefix+"-") {
					return true
				}
				continue
			}
			return true
		}
	}
	return false
}
