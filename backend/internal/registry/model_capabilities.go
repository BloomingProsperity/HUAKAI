package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

type UpsertModelCapabilityBindingParams struct {
	TenantID         int64
	Scope            string
	ModelID          int64
	Capability       string
	CapabilityValue  *string
	CapabilityParams json.RawMessage
	Enabled          bool
	Source           string
}

type ModelCapabilityBinding struct {
	ModelID          int64           `json:"model_id"`
	TenantID         *int64          `json:"tenant_id,omitempty"`
	Scope            string          `json:"scope"`
	Capability       string          `json:"capability"`
	CapabilityValue  *string         `json:"capability_value,omitempty"`
	CapabilityParams json.RawMessage `json:"capability_params,omitempty"`
	Enabled          bool            `json:"enabled"`
	Source           string          `json:"source"`
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

func (r *PostgresRegistry) UpsertModelCapabilityBinding(ctx context.Context, params UpsertModelCapabilityBindingParams) (ModelCapabilityBinding, error) {
	if r == nil || r.pool == nil {
		return ModelCapabilityBinding{}, ErrRegistryBackend
	}
	return upsertModelCapabilityBinding(ctx, r.pool, params)
}

func (r *PostgresRegistry) ListModelCapabilityBindings(ctx context.Context, modelID int64) ([]ModelCapabilityBinding, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	if modelID <= 0 {
		return nil, ErrUnknownModel
	}
	rows, err := r.pool.Query(ctx, `
SELECT model_id, tenant_id, scope, capability, capability_value, capability_params, enabled, source
FROM model_registry_capabilities
WHERE model_id = $1
  AND deleted_at IS NULL
ORDER BY scope, capability
`, modelID)
	if err != nil {
		return nil, fmt.Errorf("%w: list model capability bindings: %v", ErrRegistryBackend, err)
	}
	defer rows.Close()

	out := []ModelCapabilityBinding{}
	for rows.Next() {
		var binding ModelCapabilityBinding
		var tenantID pgtype.Int8
		var paramsRaw []byte
		if err := rows.Scan(
			&binding.ModelID,
			&tenantID,
			&binding.Scope,
			&binding.Capability,
			&binding.CapabilityValue,
			&paramsRaw,
			&binding.Enabled,
			&binding.Source,
		); err != nil {
			return nil, fmt.Errorf("%w: scan model capability binding: %v", ErrRegistryBackend, err)
		}
		if tenantID.Valid {
			value := tenantID.Int64
			binding.TenantID = &value
		}
		binding.CapabilityParams = normalizeCapabilityParamsRaw(paramsRaw)
		out = append(out, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: model capability binding rows: %v", ErrRegistryBackend, err)
	}
	return out, nil
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

func upsertModelCapabilityBinding(ctx context.Context, db modelCapabilityDB, params UpsertModelCapabilityBindingParams) (ModelCapabilityBinding, error) {
	normalized, err := normalizeKnownModelCapability(params.Capability)
	if err != nil {
		return ModelCapabilityBinding{}, err
	}
	scope := strings.TrimSpace(params.Scope)
	if scope == "" {
		scope = "tenant"
	}
	if scope != "tenant" && scope != "global" {
		return ModelCapabilityBinding{}, fmt.Errorf("%w: invalid scope %q", ErrInvalidModelCapability, scope)
	}
	if params.ModelID <= 0 {
		return ModelCapabilityBinding{}, ErrUnknownModel
	}
	source := strings.TrimSpace(params.Source)
	if source == "" {
		source = "operator"
	}
	capabilityParams := normalizeCapabilityParamsRaw(params.CapabilityParams)

	var tenantArg any
	if scope == "tenant" {
		if params.TenantID <= 0 {
			return ModelCapabilityBinding{}, fmt.Errorf("%w: tenant_id required", ErrInvalidModelCapability)
		}
		tenantArg = params.TenantID
	}

	sql := `
INSERT INTO model_registry_capabilities (
    tenant_id, scope, model_id, capability, capability_value, capability_params, enabled, source
) VALUES (
    $1, $2, $3, $4, $5, $6::jsonb, $7, $8
)
ON CONFLICT (tenant_id, model_id, capability)
WHERE deleted_at IS NULL AND scope = 'tenant'
DO UPDATE SET
    capability_value = EXCLUDED.capability_value,
    capability_params = EXCLUDED.capability_params,
    enabled = EXCLUDED.enabled,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING model_id, capability, capability_value, capability_params, enabled, source
`
	args := []any{
		tenantArg,
		scope,
		params.ModelID,
		normalized,
		params.CapabilityValue,
		capabilityParams,
		params.Enabled,
		source,
	}
	if scope == "global" {
		sql = `
INSERT INTO model_registry_capabilities (
    tenant_id, scope, model_id, capability, capability_value, capability_params, enabled, source
) VALUES (
    NULL, 'global', $1, $2, $3, $4::jsonb, $5, $6
)
ON CONFLICT (model_id, capability)
WHERE deleted_at IS NULL AND scope = 'global'
DO UPDATE SET
    capability_value = EXCLUDED.capability_value,
    capability_params = EXCLUDED.capability_params,
    enabled = EXCLUDED.enabled,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING model_id, capability, capability_value, capability_params, enabled, source
`
		args = []any{
			params.ModelID,
			normalized,
			params.CapabilityValue,
			capabilityParams,
			params.Enabled,
			source,
		}
	}

	var out ModelCapabilityBinding
	var rawOut []byte
	err = db.QueryRow(ctx, sql, args...).Scan(
		&out.ModelID,
		&out.Capability,
		&out.CapabilityValue,
		&rawOut,
		&out.Enabled,
		&out.Source,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ModelCapabilityBinding{}, ErrUnknownModel
		}
		return ModelCapabilityBinding{}, fmt.Errorf("%w: upsert model capability binding: %v", ErrRegistryBackend, err)
	}
	out.Scope = scope
	if scope == "tenant" {
		tenantID := params.TenantID
		out.TenantID = &tenantID
	}
	out.CapabilityParams = normalizeCapabilityParamsRaw(rawOut)
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

func normalizeKnownModelCapability(in string) (string, error) {
	capability := strings.TrimSpace(in)
	if capability == "" {
		return "", fmt.Errorf("%w: empty capability", ErrInvalidModelCapability)
	}
	if _, ok := knownModelCapabilityBindings[capability]; !ok {
		return "", fmt.Errorf("%w: %s", ErrInvalidModelCapability, capability)
	}
	return capability, nil
}

func normalizeCapabilityParamsRaw(raw json.RawMessage) json.RawMessage {
	trimmed := []byte(strings.TrimSpace(string(raw)))
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), trimmed...)
}

var knownModelCapabilityBindings = map[string]struct{}{
	// Public model discovery descriptors.
	"vision":           {},
	"function_calling": {},
	"tool_choice":      {},
	"reasoning":        {},
	"prompt_caching":   {},
	"response_schema":  {},

	// HCSF capability families.
	"text":              {},
	"tool_use":          {},
	"tool_result":       {},
	"thinking":          {},
	"cache_control":     {},
	"structured_output": {},
	"computer_use":      {},
	"file":              {},
	"image":             {},
	"audio":             {},
	"video":             {},
	"live_session":      {},
	"batch":             {},
	"mcp_server":        {},
	"data_retention":    {},

	// Protocol capability matrix feature names.
	"text_streaming":           {},
	"parallel_tool_calls":      {},
	"structured_output_schema": {},
	"image_input":              {},
	"audio_input":              {},
	"image_output":             {},
	"max_tokens_finish_reason": {},
	"max_completion_tokens":    {},
	"stop_sequence_emit":       {},
	"cache_breakpoints":        {},
	"signature_delta":          {},
	"system_prompt_array":      {},
	"multi_role_messages":      {},
	"reasoning_summary":        {},

	// Existing registry/model-sync vocabulary and compatibility aliases.
	"stream":              {},
	"tools":               {},
	"chat":                {},
	"messages":            {},
	"responses":           {},
	"embeddings":          {},
	"rerank":              {},
	"images":              {},
	"audio_speech":        {},
	"audio_transcription": {},
	"generateContent":     {},
	"countTokens":         {},
	"embedContent":        {},
	"batchEmbedContents":  {},
}
