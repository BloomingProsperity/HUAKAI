package modelrate

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

var overlayFieldKeys = []struct {
	jsonKey string
	pick    func(RateBuckets) *decimal.Decimal
}{
	{"input_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.Input }},
	{"output_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.Output }},
	{"cache_read_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.CacheRead }},
	{"cache_creation_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.CacheCreation }},
	{"cache_creation_5m_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.CacheCreation5m }},
	{"cache_creation_1h_micro_usd", func(b RateBuckets) *decimal.Decimal { return b.CacheCreation1h }},
}

// ApplyOverrides 把覆盖叠进官方价表 JSON。每个覆盖同时写入
// providers.<vendor>.models 与顶层 models,避免热路径任一查找顺序仍吃官方旧价。
// 未声明的桶保持官方值;官方没有该模型时新建只含覆盖桶的条目。
func ApplyOverrides(official json.RawMessage, overrides []Override) (json.RawMessage, error) {
	root, err := decodeObject(official)
	if err != nil {
		return nil, fmt.Errorf("%w: official pricing_data: %v", ErrInvalidInput, err)
	}
	models, err := nestedObject(root, "models")
	if err != nil {
		return nil, err
	}
	providers, err := nestedObject(root, "providers")
	if err != nil {
		return nil, err
	}

	sorted := append([]Override(nil), overrides...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Vendor != sorted[j].Vendor {
			return sorted[i].Vendor < sorted[j].Vendor
		}
		return sorted[i].Model < sorted[j].Model
	})

	for _, item := range sorted {
		vendor := normalizeVendor(item.Vendor)
		model := normalizeModel(item.Model)
		if vendor == "" || model == "" || !item.Rates.HasAny() {
			return nil, fmt.Errorf("%w: overlay entry missing vendor, model or rate", ErrInvalidInput)
		}
		merged, err := mergeRateObject(models[model], item.Rates)
		if err != nil {
			return nil, err
		}
		models[model] = merged

		vendorObj, err := decodeObject(providers[vendor])
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s: %v", ErrInvalidInput, vendor, err)
		}
		vendorModels, err := nestedObject(vendorObj, "models")
		if err != nil {
			return nil, err
		}
		vendorMerged, err := mergeRateObject(vendorModels[model], item.Rates)
		if err != nil {
			return nil, err
		}
		vendorModels[model] = vendorMerged
		vendorObj["models"], err = json.Marshal(vendorModels)
		if err != nil {
			return nil, fmt.Errorf("%w: encode provider models: %v", ErrBackend, err)
		}
		providers[vendor], err = json.Marshal(vendorObj)
		if err != nil {
			return nil, fmt.Errorf("%w: encode provider: %v", ErrBackend, err)
		}
	}

	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("%w: encode models: %v", ErrBackend, err)
	}
	encodedProviders, err := json.Marshal(providers)
	if err != nil {
		return nil, fmt.Errorf("%w: encode providers: %v", ErrBackend, err)
	}
	root["models"] = encodedModels
	root["providers"] = encodedProviders
	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("%w: encode pricing_data: %v", ErrBackend, err)
	}
	return out, nil
}

func mergeRateObject(existing json.RawMessage, rates RateBuckets) (json.RawMessage, error) {
	obj, err := decodeObject(existing)
	if err != nil {
		return nil, fmt.Errorf("%w: rate vector: %v", ErrInvalidInput, err)
	}
	for _, field := range overlayFieldKeys {
		value := field.pick(rates)
		if value == nil {
			continue
		}
		raw, err := json.Marshal(value.String())
		if err != nil {
			return nil, fmt.Errorf("%w: encode %s: %v", ErrBackend, field.jsonKey, err)
		}
		obj[field.jsonKey] = raw
	}
	return json.Marshal(obj)
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

func nestedObject(root map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	obj, err := decodeObject(root[key])
	if err != nil {
		return nil, fmt.Errorf("%w: field %s: %v", ErrInvalidInput, key, err)
	}
	return obj, nil
}

func parseRateBuckets(raw json.RawMessage) (RateBuckets, error) {
	obj, err := decodeObject(raw)
	if err != nil {
		return RateBuckets{}, err
	}
	out := RateBuckets{}
	set := func(dst **decimal.Decimal, keys ...string) error {
		for _, key := range keys {
			value, ok := obj[key]
			if !ok {
				continue
			}
			parsed, err := decimalFromJSON(value)
			if err != nil {
				return fmt.Errorf("%w: field %s: %v", ErrInvalidInput, key, err)
			}
			*dst = &parsed
			return nil
		}
		return nil
	}
	if err := set(&out.Input, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	if err := set(&out.Output, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	if err := set(&out.CacheRead, "cache_read_micro_usd", "cache_read_rate_micro", "cached_input_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	if err := set(&out.CacheCreation, "cache_creation_micro_usd", "cache_write_rate_micro", "cache_write_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	if err := set(&out.CacheCreation5m, "cache_creation_5m_micro_usd", "cache_creation_ephemeral_5m_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	if err := set(&out.CacheCreation1h, "cache_creation_1h_micro_usd", "cache_creation_ephemeral_1h_micro_usd"); err != nil {
		return RateBuckets{}, err
	}
	return out, nil
}

func decimalFromJSON(raw json.RawMessage) (decimal.Decimal, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return decimal.NewFromString(asString)
	}
	return decimal.NewFromString(string(raw))
}

func applyBuckets(base RateBuckets, overlay RateBuckets) RateBuckets {
	out := base.Clone()
	if overlay.Input != nil {
		out.Input = cloneDecimal(overlay.Input)
	}
	if overlay.Output != nil {
		out.Output = cloneDecimal(overlay.Output)
	}
	if overlay.CacheRead != nil {
		out.CacheRead = cloneDecimal(overlay.CacheRead)
	}
	if overlay.CacheCreation != nil {
		out.CacheCreation = cloneDecimal(overlay.CacheCreation)
	}
	if overlay.CacheCreation5m != nil {
		out.CacheCreation5m = cloneDecimal(overlay.CacheCreation5m)
	}
	if overlay.CacheCreation1h != nil {
		out.CacheCreation1h = cloneDecimal(overlay.CacheCreation1h)
	}
	return out
}
