-- name: ListActiveProxiesByTenant :many
-- 周期探活候选：未删除且仍为 active 的线路。与 CRUD 分文件以免生成码超预算。
SELECT
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret,
    status, last_check_at, created_at, updated_at
FROM proxies
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND status = 'active'
ORDER BY id;

-- name: RecordProxyQuality :one
UPDATE proxies
SET
    last_check_at = NOW(),
    quality_probed_at = NOW(),
    quality_ok = sqlc.arg(quality_ok),
    quality_latency_ms = sqlc.arg(quality_latency_ms),
    quality_error_class = sqlc.arg(quality_error_class),
    quality_grade = sqlc.arg(quality_grade),
    quality_source = sqlc.arg(quality_source),
    quality_success_at = CASE
        WHEN sqlc.arg(quality_ok)::boolean THEN NOW()
        ELSE quality_success_at
    END,
    quality_success_latency_ms = CASE
        WHEN sqlc.arg(quality_ok)::boolean THEN sqlc.arg(quality_latency_ms)
        ELSE quality_success_latency_ms
    END,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at,
    quality_probed_at, quality_ok, quality_latency_ms, quality_error_class,
    quality_grade, quality_source, quality_success_at, quality_success_latency_ms,
    created_at, updated_at;
