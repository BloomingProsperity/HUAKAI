package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const modelAliasBulkImportSource = "operator_alias_bulk_import"

type BulkImportModelAliasesParams struct {
	Aliases []ModelAliasImport `json:"aliases"`
	Actor   string             `json:"actor,omitempty"`
	Reason  string             `json:"reason,omitempty"`
}

type ModelAliasImport struct {
	TenantID       int64   `json:"tenant_id,omitempty"`
	Scope          string  `json:"scope,omitempty"`
	ModelID        int64   `json:"model_id"`
	Alias          string  `json:"alias"`
	Display        string  `json:"display,omitempty"`
	Status         string  `json:"status,omitempty"`
	Source         string  `json:"source,omitempty"`
	DisabledReason *string `json:"disabled_reason,omitempty"`
}

type ModelAliasImportResult struct {
	Index   int    `json:"index"`
	Alias   string `json:"alias"`
	ModelID int64  `json:"model_id,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func (r *PostgresRegistry) BulkImportModelAliases(ctx context.Context, params BulkImportModelAliasesParams) ([]ModelAliasImportResult, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	results := make([]ModelAliasImportResult, 0, len(params.Aliases))
	for i, item := range params.Aliases {
		result := ModelAliasImportResult{
			Index:   i,
			Alias:   item.Alias,
			ModelID: item.ModelID,
		}
		normalized, err := normalizeModelAliasImport(&item)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.Alias = normalized.Alias
		result.ModelID = normalized.ModelID

		tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return nil, fmt.Errorf("%w: begin alias import row: %v", ErrRegistryBackend, err)
		}
		if err := upsertModelAliasTx(ctx, tx, normalized); err != nil {
			_ = tx.Rollback(ctx)
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		if err := bumpAliasImportSnapshot(ctx, tx, normalized, params); err != nil {
			_ = tx.Rollback(ctx)
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			result.Status = "failed"
			result.Error = fmt.Errorf("%w: commit alias import row: %v", ErrRegistryBackend, err).Error()
			results = append(results, result)
			continue
		}
		result.Status = "upserted"
		results = append(results, result)
	}
	return results, nil
}

func normalizeModelAliasImport(item *ModelAliasImport) (ModelAliasImport, error) {
	if item == nil {
		return ModelAliasImport{}, fmt.Errorf("alias row required")
	}
	out := *item
	out.Scope = strings.TrimSpace(out.Scope)
	if out.Scope == "" {
		out.Scope = "tenant"
	}
	if out.Scope != "tenant" && out.Scope != "global" {
		return ModelAliasImport{}, fmt.Errorf("invalid scope %q", out.Scope)
	}
	if out.Scope == "tenant" && out.TenantID <= 0 {
		return ModelAliasImport{}, fmt.Errorf("tenant_id must be positive for tenant alias")
	}
	if out.ModelID <= 0 {
		return ModelAliasImport{}, fmt.Errorf("model_id must be positive")
	}
	displayDefault := strings.TrimSpace(out.Alias)
	out.Alias = AliasNormalize(out.Alias)
	if out.Alias == "" {
		return ModelAliasImport{}, fmt.Errorf("alias must be non-empty")
	}
	out.Display = strings.TrimSpace(out.Display)
	if out.Display == "" {
		out.Display = displayDefault
	}
	out.Status = strings.TrimSpace(out.Status)
	if out.Status == "" {
		out.Status = "active"
	}
	if out.Status != "active" && out.Status != "disabled" {
		return ModelAliasImport{}, fmt.Errorf("status must be active or disabled")
	}
	out.Source = strings.TrimSpace(out.Source)
	if out.Source == "" {
		out.Source = modelAliasBulkImportSource
	}
	return out, nil
}

func upsertModelAliasTx(ctx context.Context, tx pgx.Tx, item ModelAliasImport) error {
	if item.Scope == "global" {
		return upsertGlobalModelAliasTx(ctx, tx, item)
	}
	return upsertTenantModelAliasTx(ctx, tx, item)
}

func upsertTenantModelAliasTx(ctx context.Context, tx pgx.Tx, item ModelAliasImport) error {
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO model_aliases (
    tenant_id, scope, model_id, public_alias_normalized, public_alias_display,
    status, disabled_reason, source
)
SELECT $1, 'tenant', m.id, $3, $4, $5, $6, $7
FROM models m
WHERE m.id = $2
  AND m.deleted_at IS NULL
  AND (
        (m.scope = 'tenant' AND m.tenant_id = $1)
        OR (m.scope = 'global' AND m.tenant_id IS NULL)
      )
ON CONFLICT (tenant_id, public_alias_normalized)
WHERE deleted_at IS NULL AND scope = 'tenant'
DO UPDATE SET
    model_id = EXCLUDED.model_id,
    public_alias_display = EXCLUDED.public_alias_display,
    status = EXCLUDED.status,
    disabled_reason = EXCLUDED.disabled_reason,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING id
`, item.TenantID, item.ModelID, item.Alias, item.Display, item.Status, item.DisabledReason, item.Source).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUnknownModel
		}
		return fmt.Errorf("%w: upsert tenant alias: %v", ErrRegistryBackend, err)
	}
	return nil
}

func upsertGlobalModelAliasTx(ctx context.Context, tx pgx.Tx, item ModelAliasImport) error {
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO model_aliases (
    tenant_id, scope, model_id, public_alias_normalized, public_alias_display,
    status, disabled_reason, source
)
SELECT NULL, 'global', m.id, $2, $3, $4, $5, $6
FROM models m
WHERE m.id = $1
  AND m.deleted_at IS NULL
  AND m.scope = 'global'
  AND m.tenant_id IS NULL
ON CONFLICT (public_alias_normalized)
WHERE deleted_at IS NULL AND scope = 'global'
DO UPDATE SET
    model_id = EXCLUDED.model_id,
    public_alias_display = EXCLUDED.public_alias_display,
    status = EXCLUDED.status,
    disabled_reason = EXCLUDED.disabled_reason,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING id
`, item.ModelID, item.Alias, item.Display, item.Status, item.DisabledReason, item.Source).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUnknownModel
		}
		return fmt.Errorf("%w: upsert global alias: %v", ErrRegistryBackend, err)
	}
	return nil
}

func bumpAliasImportSnapshot(ctx context.Context, tx pgx.Tx, item ModelAliasImport, params BulkImportModelAliasesParams) error {
	reason := strings.TrimSpace(params.Reason)
	if reason == "" {
		reason = "model alias bulk import"
	}
	actor := strings.TrimSpace(params.Actor)
	if actor == "" {
		actor = "admin"
	}
	if item.Scope == "global" {
		_, err := bumpAffectedSnapshots(ctx, tx, []int64{item.ModelID}, reason, actor)
		return err
	}
	_, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
    version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()
`, item.TenantID, reason, actor)
	if err != nil {
		return fmt.Errorf("%w: bump alias import snapshot: %v", ErrRegistryBackend, err)
	}
	return nil
}
