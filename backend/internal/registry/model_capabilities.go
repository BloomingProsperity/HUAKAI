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
	Actor            string
	ActorRole        string
	RequestID        string
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
	normalizedScope, err := normalizeModelCapabilityBindingTarget(params)
	if err != nil {
		return ModelCapabilityBinding{}, err
	}
	params.Scope = normalizedScope
	params.Actor = strings.TrimSpace(params.Actor)
	params.ActorRole = strings.TrimSpace(params.ActorRole)
	params.RequestID = strings.TrimSpace(params.RequestID)
	if params.Actor == "" || params.ActorRole != "platform_admin" {
		return ModelCapabilityBinding{}, fmt.Errorf("%w: model capability binding operator metadata unavailable", ErrRegistryBackend)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ModelCapabilityBinding{}, fmt.Errorf("%w: begin model capability binding update: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := checkModelCapabilityTarget(ctx, tx, params); err != nil {
		return ModelCapabilityBinding{}, err
	}
	binding, err := upsertModelCapabilityBinding(ctx, tx, params)
	if err != nil {
		return ModelCapabilityBinding{}, err
	}
	if err := bumpModelCapabilitySnapshot(ctx, tx, binding, params.Actor); err != nil {
		return ModelCapabilityBinding{}, err
	}
	if err := insertModelCapabilityBindingLog(ctx, tx, binding, params); err != nil {
		return ModelCapabilityBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelCapabilityBinding{}, fmt.Errorf("%w: commit model capability binding update: %v", ErrRegistryBackend, err)
	}
	return binding, nil
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

func checkModelCapabilityTarget(ctx context.Context, tx pgx.Tx, params UpsertModelCapabilityBindingParams) error {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1
FROM models
WHERE id = $1
  AND deleted_at IS NULL
  AND (
        ($2 = 'global' AND scope = 'global' AND tenant_id IS NULL)
     OR ($2 = 'tenant' AND (
            (scope = 'global' AND tenant_id IS NULL)
         OR (scope = 'tenant' AND tenant_id = $3)
        ))
      )
FOR SHARE
`, params.ModelID, params.Scope, params.TenantID).Scan(&one)
	if err == pgx.ErrNoRows {
		return ErrUnknownModel
	}
	if err != nil {
		return fmt.Errorf("%w: check model capability target: %v", ErrRegistryBackend, err)
	}
	return nil
}

func normalizeModelCapabilityBindingTarget(params UpsertModelCapabilityBindingParams) (string, error) {
	if params.ModelID <= 0 {
		return "", ErrUnknownModel
	}
	if _, err := normalizeKnownModelCapability(params.Capability); err != nil {
		return "", err
	}
	scope := strings.TrimSpace(params.Scope)
	if scope == "" {
		scope = "tenant"
	}
	if scope != "tenant" && scope != "global" {
		return "", fmt.Errorf("%w: invalid scope %q", ErrInvalidModelCapability, scope)
	}
	if scope == "tenant" && params.TenantID <= 0 {
		return "", fmt.Errorf("%w: tenant_id required", ErrInvalidModelCapability)
	}
	if scope == "global" && params.TenantID != 0 {
		return "", fmt.Errorf("%w: tenant_id must be omitted for global scope", ErrInvalidModelCapability)
	}
	return scope, nil
}

func bumpModelCapabilitySnapshot(ctx context.Context, tx pgx.Tx, binding ModelCapabilityBinding, actor string) error {
	const reason = "model capability binding update"
	if binding.Scope == "global" {
		_, err := bumpAffectedSnapshots(ctx, tx, []int64{binding.ModelID}, reason, actor)
		return err
	}
	if binding.TenantID == nil || *binding.TenantID <= 0 {
		return fmt.Errorf("%w: tenant capability binding missing tenant", ErrRegistryBackend)
	}
	_, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
    version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()
`, *binding.TenantID, reason, actor)
	if err != nil {
		return fmt.Errorf("%w: bump model capability snapshot: %v", ErrRegistryBackend, err)
	}
	return nil
}

func insertModelCapabilityBindingLog(
	ctx context.Context,
	tx pgx.Tx,
	binding ModelCapabilityBinding,
	params UpsertModelCapabilityBindingParams,
) error {
	payload, err := json.Marshal(map[string]any{
		"model_id":             binding.ModelID,
		"scope":                binding.Scope,
		"capability":           binding.Capability,
		"enabled":              binding.Enabled,
		"source":               binding.Source,
		"has_capability_value": binding.CapabilityValue != nil,
	})
	if err != nil {
		return fmt.Errorf("%w: encode model capability binding log: %v", ErrRegistryBackend, err)
	}
	var tenantID any
	if binding.TenantID != nil {
		tenantID = *binding.TenantID
	}
	_, err = tx.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id,
    request_id, payload, log_category
) VALUES (
    $1, $2, $3, 'update_model_capability_binding', 'model_capability_binding', $4,
    NULLIF($5, ''), $6::jsonb, 'operation'
)
`, tenantID, params.Actor, params.ActorRole, binding.ModelID, params.RequestID, payload)
	if err != nil {
		return fmt.Errorf("%w: insert model capability binding log: %v", ErrRegistryBackend, err)
	}
	return nil
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
	// 公开 model 发现描述符。
	"vision":           {},
	"function_calling": {},
	"tool_choice":      {},
	"reasoning":        {},
	"prompt_caching":   {},
	"response_schema":  {},

	// HCSF 能力族。
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

	// 协议能力 matrix 的 feature 名。
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

	// 现有 registry/model-sync 词表与兼容性别名。
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
	"moderation":          {},
	"generateContent":     {},
	"countTokens":         {},
	"embedContent":        {},
	"batchEmbedContents":  {},
}
