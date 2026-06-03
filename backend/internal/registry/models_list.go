package registry

import (
	"context"
	"fmt"
	"time"
)

// ListedModel is the minimal OpenAI-compatible discovery projection for a
// model alias visible to a tenant.
type ListedModel struct {
	ID            string
	CreatedAt     time.Time
	OwnedBy       string
	ContextWindow int
	CanonicalID   string
}

const listModelsQuery = `
WITH visible_aliases AS (
    SELECT
        BTRIM(a.public_alias_display) AS id,
        a.public_alias_normalized AS sort_key,
        a.model_id,
        COALESCE(m.model_created_at, a.created_at) AS created_at,
        COALESCE(NULLIF(BTRIM(m.model_owner), ''), 'HUAKAI') AS owned_by,
        m.default_context_window AS context_window,
        m.canonical_id AS canonical_id
    FROM model_aliases a
    INNER JOIN models m
        ON m.id = a.model_id
       AND m.deleted_at IS NULL
       AND m.status = 'active'
       AND (
            (m.scope = 'tenant' AND m.tenant_id = $1)
            OR (m.scope = 'global' AND m.tenant_id IS NULL)
       )
    WHERE a.tenant_id = $1
      AND a.scope = 'tenant'
      AND a.status = 'active'
      AND a.deleted_at IS NULL
      AND BTRIM(a.public_alias_display) <> ''

    UNION ALL

    SELECT
        BTRIM(a.public_alias_display) AS id,
        a.public_alias_normalized AS sort_key,
        a.model_id,
        COALESCE(m.model_created_at, a.created_at) AS created_at,
        COALESCE(NULLIF(BTRIM(m.model_owner), ''), 'HUAKAI') AS owned_by,
        m.default_context_window AS context_window,
        m.canonical_id AS canonical_id
    FROM model_aliases a
    INNER JOIN models m
        ON m.id = a.model_id
       AND m.deleted_at IS NULL
       AND m.status = 'active'
       AND m.scope = 'global'
       AND m.tenant_id IS NULL
    INNER JOIN model_registry_tenant_policies p
        ON p.tenant_id = $1
       AND p.inherit_global_catalog = true
    WHERE a.scope = 'global'
      AND a.tenant_id IS NULL
      AND a.status = 'active'
      AND a.deleted_at IS NULL
      AND BTRIM(a.public_alias_display) <> ''
      AND NOT EXISTS (
          SELECT 1
          FROM model_aliases tenant_alias
          WHERE tenant_alias.tenant_id = $1
            AND tenant_alias.scope = 'tenant'
            AND tenant_alias.public_alias_normalized = a.public_alias_normalized
            AND tenant_alias.deleted_at IS NULL
      )
)
SELECT
    id,
    created_at,
    owned_by,
    context_window,
    canonical_id
FROM visible_aliases va
WHERE EXISTS (
    SELECT 1
    FROM model_pool_bindings mpb
    INNER JOIN pool_groups pg
        ON pg.id = mpb.pool_group_id
       AND pg.tenant_id = mpb.tenant_id
       AND pg.enabled = true
       AND pg.deleted_at IS NULL
    WHERE mpb.tenant_id = $1
      AND mpb.model_id = va.model_id
      AND mpb.deleted_at IS NULL
      AND mpb.enabled = true
      AND (mpb.effective_from IS NULL OR mpb.effective_from <= now())
      AND (mpb.effective_until IS NULL OR mpb.effective_until > now())
)
ORDER BY sort_key ASC, id ASC;
`

// ListModels returns the OpenAI-compatible model discovery projection for one
// tenant. It is SELECT-only and does not read credentials or billing state.
func (r *PostgresRegistry) ListModels(ctx context.Context, tenantID int64) ([]ListedModel, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	rows, err := r.pool.Query(ctx, listModelsQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: list models: %v", ErrRegistryBackend, err)
	}
	defer rows.Close()

	models := make([]ListedModel, 0)
	for rows.Next() {
		var model ListedModel
		if err := rows.Scan(&model.ID, &model.CreatedAt, &model.OwnedBy, &model.ContextWindow, &model.CanonicalID); err != nil {
			return nil, fmt.Errorf("%w: scan model: %v", ErrRegistryBackend, err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: rows: %v", ErrRegistryBackend, err)
	}
	return models, nil
}
