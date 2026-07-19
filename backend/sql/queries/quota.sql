-- HUAKAI 配额策略读取。所有查询都显式携带 tenant_id。

-- name: ListActiveQuotaPoliciesForScopes :many
-- Reserve 前按租户、scope、模型与 metric 取当前可用策略。
SELECT
    tenant_id,
    id,
    scope_kind,
    scope_id,
    model_selector,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    burst_value,
    mode,
    priority,
    valid_from,
    valid_until
FROM quota_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND enabled = true
  AND mode <> 'disabled'
  AND (scope_kind, scope_id) IN (
      SELECT requested_scope.kind, requested_scope.id
      FROM jsonb_to_recordset(sqlc.arg(scopes)::jsonb)
          AS requested_scope(kind text, id text)
  )
  AND (model_selector = '*' OR model_selector = sqlc.arg(requested_model)::text)
  AND metric = ANY(sqlc.arg(metrics)::text[])
  AND valid_from <= sqlc.arg(at_time)::timestamptz
  AND (valid_until IS NULL OR valid_until > sqlc.arg(at_time)::timestamptz)
ORDER BY
    CASE WHEN model_selector = '*' THEN 0 ELSE 1 END,
    priority ASC,
    id ASC
FOR UPDATE;

-- name: ListCurrentQuotaWindowsForScope :many
-- 返回一个 scope 的当前窗口明细；model_selector 为空时返回全部模型维度。
SELECT
    qp.tenant_id,
    qp.id AS policy_id,
    qp.scope_kind,
    qp.scope_id,
    qp.model_selector,
    qp.metric,
    qp.window_kind,
    qp.window_seconds,
    qp.limit_value,
    qp.burst_value,
    qp.mode,
    qp.priority,
    qp.valid_from,
    qp.valid_until,
    COALESCE(qw.id, 0)::bigint AS window_id,
    qw.window_start,
    qw.window_end,
    COALESCE(qw.reserved_value, 0)::numeric(20,8) AS reserved_value,
    COALESCE(qw.settled_value, 0)::numeric(20,8) AS settled_value,
    COALESCE(qw.overage_value, 0)::numeric(20,8) AS overage_value,
    COALESCE(qw.request_count, 0)::bigint AS request_count,
    COALESCE(qw.version, 0)::integer AS version
FROM quota_policies qp
LEFT JOIN quota_windows qw
  ON qw.tenant_id = qp.tenant_id
 AND qw.policy_id = qp.id
 AND qw.window_start <= sqlc.arg(at_time)::timestamptz
 AND qw.window_end > sqlc.arg(at_time)::timestamptz
WHERE qp.tenant_id = sqlc.arg(tenant_id)::bigint
  AND qp.enabled = true
  AND qp.mode <> 'disabled'
  AND qp.scope_kind = sqlc.arg(scope_kind)::text
  AND qp.scope_id = sqlc.arg(scope_id)::text
  AND (sqlc.narg(model_selector)::text IS NULL OR qp.model_selector = sqlc.narg(model_selector)::text)
  AND qp.metric = ANY(sqlc.arg(metrics)::text[])
  AND qp.valid_from <= sqlc.arg(at_time)::timestamptz
  AND (qp.valid_until IS NULL OR qp.valid_until > sqlc.arg(at_time)::timestamptz)
ORDER BY
    CASE WHEN qp.model_selector = '*' THEN 0 ELSE 1 END,
    CASE qp.window_kind
        WHEN 'calendar_day' THEN 1
        WHEN 'calendar_week' THEN 2
        WHEN 'calendar_month' THEN 3
        ELSE 100
    END,
    qp.priority ASC,
    qp.id ASC;
