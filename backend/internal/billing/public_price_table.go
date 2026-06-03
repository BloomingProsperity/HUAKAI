package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

var publicPriceMicroDivisor = decimal.NewFromInt(1_000_000)

// PublicPrice is the customer-facing per-token USD price for model discovery.
// It deliberately excludes internal cost, cache-tier, markup, and multiplier
// details from the public shape.
type PublicPrice struct {
	InputPerToken  decimal.Decimal
	OutputPerToken decimal.Decimal
	HasInput       bool
	HasOutput      bool
}

type PublicPriceTable struct {
	Version string
	prices  map[string]PublicPrice
}

func NewPublicPriceTable(version string, prices map[string]PublicPrice) PublicPriceTable {
	out := PublicPriceTable{
		Version: strings.TrimSpace(version),
		prices:  make(map[string]PublicPrice, len(prices)),
	}
	for key, price := range prices {
		normalized := normalizePublicPriceKey(key)
		if normalized == "" || (!price.HasInput && !price.HasOutput) {
			continue
		}
		out.prices[normalized] = price
	}
	return out
}

func (t PublicPriceTable) Lookup(candidates ...string) (PublicPrice, bool) {
	for _, candidate := range candidates {
		key := normalizePublicPriceKey(candidate)
		if key == "" {
			continue
		}
		price, ok := t.prices[key]
		if ok && (price.HasInput || price.HasOutput) {
			return price, true
		}
	}
	return PublicPrice{}, false
}

const currentPublicRateTableSQL = `
SELECT id, version, pricing_data, effective_from, effective_to, created_at
FROM billing_pricing_versions
WHERE tenant_id = $1
  AND is_public = true
  AND effective_to IS NULL
ORDER BY effective_from DESC
LIMIT 1`

func (s *PGXRateTableSource) PublicModelPrices(ctx context.Context, tenantID int64) (PublicPriceTable, error) {
	if s == nil || s.pool == nil {
		return PublicPriceTable{}, ErrPoolNotConfigured
	}
	table, err := s.currentPublicRateTable(ctx, tenantID)
	if errors.Is(err, ErrRateTableNotFound) && tenantID != 0 {
		table, err = s.currentPublicRateTable(ctx, 0)
	}
	if errors.Is(err, ErrRateTableNotFound) {
		return PublicPriceTable{}, nil
	}
	if err != nil {
		return PublicPriceTable{}, err
	}
	return parsePublicPriceTable(table.Version, table.PricingData)
}

func (s *PGXRateTableSource) currentPublicRateTable(ctx context.Context, tenantID int64) (RateTable, error) {
	var (
		row           RateTable
		effectiveFrom pgtype.Timestamptz
		effectiveTo   pgtype.Timestamptz
		createdAt     pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, currentPublicRateTableSQL, tenantID).Scan(&row.ID, &row.Version, &row.PricingData, &effectiveFrom, &effectiveTo, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RateTable{}, ErrRateTableNotFound
	}
	if err != nil {
		return RateTable{}, fmt.Errorf("billing: get current public model prices: %w", err)
	}
	row.EffectiveFrom = pgTime(effectiveFrom)
	row.EffectiveTo = pgTimePtr(effectiveTo)
	row.CreatedAt = pgTime(createdAt)
	if len(row.PricingData) == 0 {
		row.PricingData = json.RawMessage(`{}`)
	}
	return row, nil
}

func parsePublicPriceTable(version string, raw json.RawMessage) (PublicPriceTable, error) {
	root := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return NewPublicPriceTable(version, nil), nil
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return PublicPriceTable{}, fmt.Errorf("billing: public price table invalid: %w", err)
	}
	entries := map[string]json.RawMessage{}
	if modelsRaw, ok := publicPriceRawField(root, "models"); ok {
		if err := json.Unmarshal(modelsRaw, &entries); err != nil {
			return PublicPriceTable{}, fmt.Errorf("billing: public price models invalid: %w", err)
		}
	} else if looksLikePublicRateVector(root) {
		entries["default"] = raw
		entries["*"] = raw
	} else {
		for key, value := range root {
			var candidate map[string]json.RawMessage
			if err := json.Unmarshal(value, &candidate); err != nil {
				continue
			}
			if looksLikePublicRateVector(candidate) {
				entries[key] = value
			}
		}
	}

	prices := make(map[string]PublicPrice, len(entries))
	for key, value := range entries {
		price, ok, err := parsePublicPrice(value)
		if err != nil {
			return PublicPriceTable{}, err
		}
		if ok {
			prices[key] = price
		}
	}
	return NewPublicPriceTable(version, prices), nil
}

func parsePublicPrice(raw json.RawMessage) (PublicPrice, bool, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return PublicPrice{}, false, fmt.Errorf("billing: public price vector invalid: %w", err)
	}
	multiplier := decimal.NewFromInt(1)
	if value, ok, err := publicPriceDecimalField(obj, "model_multiplier", "multiplier"); err != nil {
		return PublicPrice{}, false, err
	} else if ok {
		if !value.IsPositive() {
			return PublicPrice{}, false, nil
		}
		multiplier = value
	}

	input, hasInput, err := publicPriceDecimalField(obj, "input_micro_usd", "input_rate_micro", "input_cost_micro_usd", "input_per_token_micro_usd")
	if err != nil {
		return PublicPrice{}, false, err
	}
	output, hasOutput, err := publicPriceDecimalField(obj, "output_micro_usd", "output_rate_micro", "output_cost_micro_usd", "output_per_token_micro_usd")
	if err != nil {
		return PublicPrice{}, false, err
	}
	if !hasInput && !hasOutput {
		return PublicPrice{}, false, nil
	}
	out := PublicPrice{HasInput: hasInput, HasOutput: hasOutput}
	if hasInput {
		if input.IsNegative() {
			return PublicPrice{}, false, errors.New("billing: public input price must not be negative")
		}
		out.InputPerToken = input.Mul(multiplier).Div(publicPriceMicroDivisor)
	}
	if hasOutput {
		if output.IsNegative() {
			return PublicPrice{}, false, errors.New("billing: public output price must not be negative")
		}
		out.OutputPerToken = output.Mul(multiplier).Div(publicPriceMicroDivisor)
	}
	return out, true, nil
}

func publicPriceDecimalField(obj map[string]json.RawMessage, keys ...string) (decimal.Decimal, bool, error) {
	for _, key := range keys {
		raw, ok := publicPriceRawField(obj, key)
		if !ok {
			continue
		}
		value, err := publicPriceDecimal(raw)
		if err != nil {
			return decimal.Zero, false, fmt.Errorf("billing: public price field %s invalid: %w", key, err)
		}
		return value, true, nil
	}
	return decimal.Zero, false, nil
}

func publicPriceDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return decimal.NewFromString(strings.TrimSpace(s))
	}
	return decimal.NewFromString(strings.TrimSpace(string(raw)))
}

func publicPriceRawField(obj map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := obj[key]; ok {
		return raw, true
	}
	normalized := normalizePublicPriceKey(key)
	for candidate, raw := range obj {
		if normalizePublicPriceKey(candidate) == normalized {
			return raw, true
		}
	}
	return nil, false
}

func looksLikePublicRateVector(obj map[string]json.RawMessage) bool {
	for _, key := range []string{
		"input_micro_usd",
		"input_rate_micro",
		"input_cost_micro_usd",
		"input_per_token_micro_usd",
		"output_micro_usd",
		"output_rate_micro",
		"output_cost_micro_usd",
		"output_per_token_micro_usd",
	} {
		if _, ok := publicPriceRawField(obj, key); ok {
			return true
		}
	}
	return false
}

func normalizePublicPriceKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, "/", "_")
	return v
}
