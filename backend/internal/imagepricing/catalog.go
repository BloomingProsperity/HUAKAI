package imagepricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

type Scheme string

const (
	SchemePerImage   Scheme = "per_image"
	SchemeTokenImage Scheme = "token_image"
)

type AmountRangeValue struct {
	Min int
	Max int
}

type TokenRates struct {
	Raw        json.RawMessage
	Input      decimal.Decimal
	Output     decimal.Decimal
	Multiplier decimal.Decimal
	HasInput   bool
	HasOutput  bool
}

type Catalog struct {
	raw json.RawMessage
}

func NewCatalog(raw json.RawMessage) (*Catalog, error) {
	if len(raw) == 0 {
		return nil, errors.New("imagepricing: rate table empty")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	return &Catalog{raw: raw}, nil
}

func (c *Catalog) SchemeFor(provider string, models []string) (Scheme, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return "", err
	}
	var scheme string
	if raw, ok := rawField(entry.obj, "pricing_scheme"); ok {
		_ = json.Unmarshal(raw, &scheme)
	}
	scheme = strings.TrimSpace(scheme)
	switch Scheme(scheme) {
	case SchemePerImage, SchemeTokenImage:
		return Scheme(scheme), nil
	default:
		return "", fmt.Errorf("imagepricing: unsupported pricing_scheme %q", scheme)
	}
}

func (c *Catalog) PerImageMicroUSD(provider string, models []string, size, quality string) (decimal.Decimal, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return decimal.Zero, err
	}
	if scheme, err := c.SchemeFor(provider, models); err != nil {
		return decimal.Zero, err
	} else if scheme != SchemePerImage {
		return decimal.Zero, fmt.Errorf("imagepricing: model is %s", scheme)
	}
	base, ok, err := decimalField(entry.obj, "image_base_micro_usd")
	if err != nil {
		return decimal.Zero, err
	}
	if !ok {
		return decimal.Zero, errors.New("imagepricing: image_base_micro_usd missing")
	}
	sizeMultiplier, err := multiplierFor(entry.obj, "image_size_multipliers", size)
	if err != nil {
		return decimal.Zero, err
	}
	qualityMultiplier, err := qualityMultiplierFor(entry.obj, quality, size)
	if err != nil {
		return decimal.Zero, err
	}
	return base.Mul(sizeMultiplier).Mul(qualityMultiplier), nil
}

func (c *Catalog) AllowedSizes(provider string, models []string) ([]string, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return nil, err
	}
	raw, ok := rawField(entry.obj, "image_size_multipliers")
	if !ok {
		return nil, errors.New("imagepricing: image_size_multipliers missing")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(obj))
	for key := range obj {
		key = strings.TrimSpace(key)
		if key != "" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, errors.New("imagepricing: allowed sizes empty")
	}
	return out, nil
}

func (c *Catalog) AmountRange(provider string, models []string) (AmountRangeValue, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return AmountRangeValue{}, err
	}
	raw, ok := rawField(entry.obj, "image_amount_range")
	if !ok {
		return AmountRangeValue{}, errors.New("imagepricing: image_amount_range missing")
	}
	var rng struct {
		Min int `json:"min"`
		Max int `json:"max"`
	}
	if err := json.Unmarshal(raw, &rng); err != nil {
		return AmountRangeValue{}, err
	}
	if rng.Min <= 0 || rng.Max < rng.Min {
		return AmountRangeValue{}, errors.New("imagepricing: invalid image_amount_range")
	}
	return AmountRangeValue{Min: rng.Min, Max: rng.Max}, nil
}

func (c *Catalog) PromptMaxChars(provider string, models []string) (int, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return 0, err
	}
	raw, ok := rawField(entry.obj, "image_prompt_max_chars")
	if !ok {
		return 0, nil
	}
	var max int
	if err := json.Unmarshal(raw, &max); err != nil {
		return 0, err
	}
	if max < 0 {
		return 0, errors.New("imagepricing: image_prompt_max_chars negative")
	}
	return max, nil
}

func (c *Catalog) OutputTokenUpperBound(provider string, models []string, size string) (int, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return 0, err
	}
	for _, key := range []string{"image_output_token_upper_bound", "image_output_token_upper_bounds"} {
		raw, ok := rawField(entry.obj, key)
		if !ok {
			continue
		}
		var bySize map[string]int
		if err := json.Unmarshal(raw, &bySize); err == nil {
			if n := bySize[strings.TrimSpace(size)]; n > 0 {
				return n, nil
			}
		}
		var n int
		if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, errors.New("imagepricing: image output token upper bound missing")
}

func (c *Catalog) TokenRates(provider string, models []string) (TokenRates, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return TokenRates{}, err
	}
	input, hasInput, err := decimalField(entry.obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return TokenRates{}, err
	}
	output, hasOutput, err := decimalField(entry.obj, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd", "output_per_token_micro_usd")
	if err != nil {
		return TokenRates{}, err
	}
	multiplier := decimal.NewFromInt(1)
	if value, ok, err := decimalField(entry.obj, "model_multiplier", "multiplier"); err != nil {
		return TokenRates{}, err
	} else if ok {
		multiplier = value
	}
	if !hasInput || !hasOutput {
		return TokenRates{}, errors.New("imagepricing: token-image input/output rate missing")
	}
	return TokenRates{Raw: entry.raw, Input: input, Output: output, Multiplier: multiplier, HasInput: hasInput, HasOutput: hasOutput}, nil
}

type modelEntry struct {
	raw json.RawMessage
	obj map[string]json.RawMessage
}

func (c *Catalog) entry(provider string, models []string) (modelEntry, error) {
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(c.raw, &root); err != nil {
		return modelEntry{}, err
	}
	if providersRaw, ok := rawField(root, "providers"); ok {
		if providerRaw, ok := namedRaw(providersRaw, []string{provider}); ok {
			if rateRaw, ok := modelRaw(providerRaw, models); ok {
				return parseEntry(rateRaw)
			}
		}
	}
	if modelsRaw, ok := rawField(root, "models"); ok {
		if rateRaw, ok := namedRaw(modelsRaw, models); ok {
			return parseEntry(rateRaw)
		}
	}
	if rateRaw, ok := namedRaw(c.raw, models); ok {
		return parseEntry(rateRaw)
	}
	if _, ok := rawField(root, "pricing_scheme"); ok {
		return parseEntry(c.raw)
	}
	return modelEntry{}, errors.New("imagepricing: image model pricing missing")
}

func parseEntry(raw json.RawMessage) (modelEntry, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return modelEntry{}, err
	}
	return modelEntry{raw: raw, obj: obj}, nil
}

func multiplierFor(obj map[string]json.RawMessage, field, name string) (decimal.Decimal, error) {
	raw, ok := rawField(obj, field)
	if !ok {
		return decimal.Zero, fmt.Errorf("imagepricing: %s missing", field)
	}
	var byName map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byName); err != nil {
		return decimal.Zero, err
	}
	value, ok := namedRaw(raw, []string{name})
	if !ok {
		return decimal.Zero, fmt.Errorf("imagepricing: multiplier missing for %s", name)
	}
	return parseDecimal(value)
}

func qualityMultiplierFor(obj map[string]json.RawMessage, quality, size string) (decimal.Decimal, error) {
	quality = strings.TrimSpace(quality)
	if quality == "" {
		quality = "standard"
	}
	raw, ok := rawField(obj, "image_quality_multipliers")
	if !ok {
		if quality == "standard" {
			return decimal.NewFromInt(1), nil
		}
		return decimal.Zero, errors.New("imagepricing: image_quality_multipliers missing")
	}
	for _, candidate := range []string{quality + "@" + strings.TrimSpace(size), quality, "standard"} {
		if value, ok := namedRaw(raw, []string{candidate}); ok {
			return parseDecimal(value)
		}
	}
	return decimal.Zero, fmt.Errorf("imagepricing: quality multiplier missing for %s", quality)
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
		name = strings.TrimSpace(name)
		if name == "" {
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

func normalizeKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}
