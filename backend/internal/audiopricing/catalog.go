package audiopricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type Scheme string

const (
	SchemePerChar   Scheme = "per_char"
	SchemePerSecond Scheme = "per_second"
	SchemeToken     Scheme = "token"
)

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
		return nil, errors.New("audiopricing: rate table empty")
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
	switch Scheme(strings.TrimSpace(scheme)) {
	case SchemePerChar, SchemePerSecond, SchemeToken:
		return Scheme(strings.TrimSpace(scheme)), nil
	default:
		return "", fmt.Errorf("audiopricing: unsupported pricing_scheme %q", strings.TrimSpace(scheme))
	}
}

func (c *Catalog) CharMicroUSD(provider string, models []string) (decimal.Decimal, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return decimal.Zero, err
	}
	if scheme, err := c.SchemeFor(provider, models); err != nil {
		return decimal.Zero, err
	} else if scheme != SchemePerChar {
		return decimal.Zero, fmt.Errorf("audiopricing: model is %s", scheme)
	}
	return requiredPositiveRate(entry.obj, "input_char_micro_usd")
}

func (c *Catalog) SecondMicroUSD(provider string, models []string) (decimal.Decimal, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return decimal.Zero, err
	}
	if scheme, err := c.SchemeFor(provider, models); err != nil {
		return decimal.Zero, err
	} else if scheme != SchemePerSecond {
		return decimal.Zero, fmt.Errorf("audiopricing: model is %s", scheme)
	}
	return requiredPositiveRate(entry.obj, "input_second_micro_usd")
}

func (c *Catalog) TokenRates(provider string, models []string) (TokenRates, error) {
	entry, err := c.entry(provider, models)
	if err != nil {
		return TokenRates{}, err
	}
	if scheme, err := c.SchemeFor(provider, models); err != nil {
		return TokenRates{}, err
	} else if scheme != SchemeToken {
		return TokenRates{}, fmt.Errorf("audiopricing: model is %s", scheme)
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
		return TokenRates{}, errors.New("audiopricing: token input/output rate missing")
	}
	if input.IsNegative() || output.IsNegative() || multiplier.IsNegative() || multiplier.IsZero() {
		return TokenRates{}, errors.New("audiopricing: token rate vector invalid")
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
	return modelEntry{}, errors.New("audiopricing: audio model pricing missing")
}

func parseEntry(raw json.RawMessage) (modelEntry, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return modelEntry{}, err
	}
	return modelEntry{raw: raw, obj: obj}, nil
}

func requiredPositiveRate(obj map[string]json.RawMessage, key string) (decimal.Decimal, error) {
	value, ok, err := decimalField(obj, key)
	if err != nil {
		return decimal.Zero, err
	}
	if !ok {
		return decimal.Zero, fmt.Errorf("audiopricing: %s missing", key)
	}
	if value.IsNegative() || value.IsZero() {
		return decimal.Zero, fmt.Errorf("audiopricing: %s must be positive", key)
	}
	return value, nil
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

func normalizeKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}
