package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type UpdateModelCapabilitiesParams struct {
	ModelID         int64
	Capabilities    map[string]bool
	MaxOutputTokens *int
	ModelMode       *string
}

type ModelCapabilityUpdate struct {
	ModelID         int64
	Capabilities    map[string]bool
	MaxOutputTokens *int
	ModelMode       string
}

type modelCapabilityDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *PostgresRegistry) UpdateModelCapabilities(ctx context.Context, params UpdateModelCapabilitiesParams) (ModelCapabilityUpdate, error) {
	if r == nil || r.pool == nil {
		return ModelCapabilityUpdate{}, ErrRegistryBackend
	}
	return updateModelCapabilities(ctx, r.pool, params)
}

func updateModelCapabilities(ctx context.Context, db modelCapabilityDB, params UpdateModelCapabilitiesParams) (ModelCapabilityUpdate, error) {
	capabilities := normalizeModelCapabilityMap(params.Capabilities)
	raw, err := json.Marshal(capabilities)
	if err != nil {
		return ModelCapabilityUpdate{}, fmt.Errorf("%w: marshal model capabilities: %v", ErrRegistryBackend, err)
	}
	modeArg := normalizeOptionalModelMode(params.ModelMode)

	var out ModelCapabilityUpdate
	var rawOut []byte
	var mode *string
	err = db.QueryRow(ctx, `
UPDATE models
SET capabilities = $2::jsonb,
    max_output_tokens = $3::integer,
    model_mode = NULLIF(BTRIM($4::text), ''),
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
RETURNING id, capabilities, max_output_tokens, model_mode
`, params.ModelID, raw, params.MaxOutputTokens, modeArg).Scan(
		&out.ModelID,
		&rawOut,
		&out.MaxOutputTokens,
		&mode,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ModelCapabilityUpdate{}, ErrUnknownModel
		}
		return ModelCapabilityUpdate{}, fmt.Errorf("%w: update model capabilities: %v", ErrRegistryBackend, err)
	}
	decoded, err := decodeCapabilityUpdateMap(rawOut)
	if err != nil {
		return ModelCapabilityUpdate{}, fmt.Errorf("%w: decode model capabilities: %v", ErrRegistryBackend, err)
	}
	out.Capabilities = decoded
	if mode != nil {
		out.ModelMode = *mode
	}
	return out, nil
}

func normalizeModelCapabilityMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func normalizeOptionalModelMode(in *string) any {
	if in == nil {
		return nil
	}
	mode := strings.TrimSpace(*in)
	if mode == "" {
		return nil
	}
	return mode
}

func decodeCapabilityUpdateMap(raw []byte) (map[string]bool, error) {
	if len(raw) == 0 {
		return map[string]bool{}, nil
	}
	var out map[string]bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]bool{}
	}
	return out, nil
}
