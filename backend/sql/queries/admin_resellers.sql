-- 分销商租户作用域查询。
-- 本文件在 Slice 2 只承载身份解析所需的只读查询；分销商 CRUD 留待后续切片。

-- name: ResolveActiveSessionAdmin :one
SELECT
    u.id AS user_id,
    u.tenant_id,
    t.parent_tenant_id
FROM users u
JOIN tenants t ON t.id = u.tenant_id
WHERE u.id = sqlc.arg(user_id)::bigint
  AND u.tenant_id = sqlc.arg(tenant_id)::bigint
  AND u.deleted_at IS NULL
  AND u.status = 'active'
  AND u.role = 'admin'
  AND t.deleted_at IS NULL
  AND t.status = 'active'
LIMIT 1;

-- name: ListActiveTenantScope :many
WITH RECURSIVE tenant_scope AS (
    SELECT
        t.id,
        0::integer AS depth,
        ARRAY[t.id]::bigint[] AS visited_ids,
        false AS cycle_detected,
        (t.parent_tenant_id IS NOT NULL) AS scope_root_is_child
    FROM tenants t
    WHERE t.id = sqlc.arg(root_tenant_id)::bigint
      AND t.deleted_at IS NULL
      AND t.status = 'active'

    UNION

    SELECT
        child.id,
        parent.depth + 1,
        parent.visited_ids || child.id,
        child.id = ANY(parent.visited_ids),
        parent.scope_root_is_child
    FROM tenant_scope parent
    JOIN tenants child
      ON child.parent_tenant_id = parent.id
     AND child.deleted_at IS NULL
     AND child.status = 'active'
    WHERE parent.depth < 33
      AND NOT parent.cycle_detected
)
SELECT
    id,
    depth,
    cycle_detected,
    scope_root_is_child::boolean AS scope_root_is_child
FROM tenant_scope
ORDER BY depth, id;
