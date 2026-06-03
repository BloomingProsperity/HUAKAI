-- name: GetPoolGroupPricingRatio :one
SELECT
    id,
    tenant_id,
    pool_group_id,
    ratio::numeric(20,8)::text AS ratio,
    public_ratio,
    created_by,
    updated_by,
    created_at,
    updated_at
FROM pool_group_pricing_ratios
WHERE tenant_id = $1 AND pool_group_id = $2;

-- name: ListPoolGroupPricingRatios :many
SELECT
    id,
    tenant_id,
    pool_group_id,
    ratio::numeric(20,8)::text AS ratio,
    public_ratio,
    created_by,
    updated_by,
    created_at,
    updated_at
FROM pool_group_pricing_ratios
WHERE tenant_id = $1
ORDER BY pool_group_id ASC;

-- name: UpsertPoolGroupPricingRatio :one
INSERT INTO pool_group_pricing_ratios (
    tenant_id,
    pool_group_id,
    ratio,
    public_ratio,
    created_by,
    updated_by
) VALUES (
    sqlc.arg(tenant_id),
    sqlc.arg(pool_group_id),
    sqlc.arg(ratio)::text::numeric(20,8),
    sqlc.arg(public_ratio),
    sqlc.arg(actor),
    sqlc.arg(actor)
)
ON CONFLICT (tenant_id, pool_group_id) DO UPDATE
SET ratio = EXCLUDED.ratio,
    public_ratio = EXCLUDED.public_ratio,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING
    id,
    tenant_id,
    pool_group_id,
    ratio::numeric(20,8)::text AS ratio,
    public_ratio,
    created_by,
    updated_by,
    created_at,
    updated_at;

-- name: DeletePoolGroupPricingRatio :one
DELETE FROM pool_group_pricing_ratios
WHERE tenant_id = $1 AND pool_group_id = $2
RETURNING id;
