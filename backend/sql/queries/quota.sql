-- HUAKAI 配额子系统 Slice A sqlc 查询骨架。
-- 本文件先锁定查询边界; sqlc 生成包留到切片 B 接入实现时再加。
-- 约束: 所有读写定位都显式带 tenant_id, 防跨租户误读/误改。

-- name: ListActiveQuotaPoliciesForScopes :many
-- Reserve 前按租户、scope、metric 取当前可用策略; scopes 为 [{"kind": "...", "id": "..."}]。
SELECT
    tenant_id,
    id,
    scope_kind,
    scope_id,
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
  AND metric = ANY(sqlc.arg(metrics)::text[])
  AND valid_from <= sqlc.arg(at_time)::timestamptz
  AND (valid_until IS NULL OR valid_until > sqlc.arg(at_time)::timestamptz)
ORDER BY priority ASC, id ASC
FOR UPDATE;

-- name: ListCurrentQuotaWindowsForScope :many
-- Active quota-window read projection: policies for one tenant/scope filtered to the
-- requested metrics, plus the current window counters. Cost-only callers (subscription
-- progress, key-control) pass {cost_usd} to preserve their original behaviour; the
-- self-service /quota read passes the window-shaped metrics (requests/cost_usd/tokens).
SELECT
    qp.tenant_id,
    qp.id AS policy_id,
    qp.scope_kind,
    qp.scope_id,
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
  AND qp.metric = ANY(sqlc.arg(metrics)::text[])
  AND qp.valid_from <= sqlc.arg(at_time)::timestamptz
  AND (qp.valid_until IS NULL OR qp.valid_until > sqlc.arg(at_time)::timestamptz)
ORDER BY
    CASE qp.window_kind
        WHEN 'calendar_day' THEN 1
        WHEN 'calendar_week' THEN 2
        WHEN 'calendar_month' THEN 3
        ELSE 100
    END,
    qp.priority ASC,
    qp.id ASC;

-- name: UpsertQuotaWindow :one
-- 为 policy/window_start 建立或复用窗口行; 唯一键为 (tenant_id, policy_id, window_start)。
WITH upserted AS (
    INSERT INTO quota_windows (
        tenant_id, policy_id, window_start, window_end
    )
    SELECT
        qp.tenant_id,
        qp.id,
        sqlc.arg(window_start)::timestamptz,
        sqlc.arg(window_end)::timestamptz
    FROM quota_policies qp
    WHERE qp.tenant_id = sqlc.arg(tenant_id)::bigint
      AND qp.id = sqlc.arg(policy_id)::bigint
    ON CONFLICT ON CONSTRAINT uq_quota_windows_policy_start
    DO UPDATE SET
        window_end = EXCLUDED.window_end,
        updated_at = NOW()
    WHERE quota_windows.tenant_id = sqlc.arg(tenant_id)::bigint
    RETURNING
        tenant_id,
        id,
        policy_id,
        window_start,
        window_end,
        reserved_value,
        settled_value,
        overage_value,
        request_count,
        version
)
SELECT
    upserted.tenant_id,
    upserted.id,
    upserted.policy_id,
    upserted.window_start,
    upserted.window_end,
    upserted.reserved_value,
    upserted.settled_value,
    upserted.overage_value,
    upserted.request_count,
    upserted.version,
    qp.window_kind,
    qp.window_seconds
FROM upserted
JOIN quota_policies qp
  ON qp.tenant_id = upserted.tenant_id
 AND qp.id = upserted.policy_id;

-- name: GetQuotaWindowForUpdate :one
-- Reserve/settle 事务内锁住一个租户窗口。
SELECT
    tenant_id,
    id,
    policy_id,
    window_start,
    window_end,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version
FROM quota_windows
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
FOR UPDATE;

-- name: IncrementQuotaWindowReserved :one
-- Cost enforce: reserved + settled + delta 不得超过调用方传入的策略上限。
UPDATE quota_windows
SET reserved_value = reserved_value + sqlc.arg(reserve_delta)::numeric(20,8),
    request_count = request_count + sqlc.arg(request_count_delta)::bigint,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
  AND reserved_value + settled_value + sqlc.arg(reserve_delta)::numeric(20,8)
      <= sqlc.arg(limit_value)::numeric(20,8)
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;

-- name: IncrementQuotaWindowRequestCount :one
-- request_count 镜像辅助; Reserve 准入使用 IncrementQuotaWindowReserved 的 Model B 计数。
UPDATE quota_windows
SET request_count = request_count + sqlc.arg(request_count_delta)::bigint,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
  AND request_count + sqlc.arg(request_count_delta)::bigint
      <= sqlc.arg(limit_value)::numeric(20,8)
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;

-- name: ApplyQuotaWindowSettlement :one
-- Settle 阶段把 reserved 转为 settled, actual 超出部分进入 overage 供审计和后续拒绝。
UPDATE quota_windows
SET reserved_value = GREATEST(
        0::numeric(20,8),
        reserved_value - sqlc.arg(reserved_release_value)::numeric(20,8)
    ),
    settled_value = settled_value + sqlc.arg(settled_add_value)::numeric(20,8),
    overage_value = overage_value + sqlc.arg(overage_add_value)::numeric(20,8),
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;

-- name: GetQuotaReservationByClaimForUpdate :one
-- claim_id + tenant_id 是 reservation 幂等键。
SELECT
    tenant_id,
    id,
    claim_id,
    request_fingerprint,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    settled_cost,
    settled_units,
    overage_units,
    status,
    lease_expires_at,
    settled_at,
    released_at,
    release_reason
FROM quota_reservations
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
FOR UPDATE;

-- name: InsertQuotaReservation :one
-- 新 claim 的配额预留记录; 幂等冲突由 GetQuotaReservationByClaimForUpdate 先判定。
INSERT INTO quota_reservations (
    tenant_id,
    claim_id,
    request_fingerprint,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    lease_expires_at
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(claim_id)::bigint,
    sqlc.arg(request_fingerprint)::text,
    sqlc.arg(scope_snapshot)::jsonb,
    sqlc.arg(policy_snapshot)::jsonb,
    sqlc.arg(predicted_cost)::numeric(20,8),
    sqlc.arg(reserved_units)::numeric(20,8),
    sqlc.arg(lease_expires_at)::timestamptz
WHERE EXISTS (
    SELECT 1
    FROM billing_ledger_claims blc
    WHERE blc.tenant_id = sqlc.arg(tenant_id)::bigint
      AND blc.id = sqlc.arg(claim_id)::bigint
)
RETURNING
    tenant_id,
    id,
    claim_id,
    request_fingerprint,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    status,
    lease_expires_at,
    created_at,
    updated_at;

-- name: ReactivateQuotaReservation :one
-- released/expired claim 重试通过重新评估后, 复用原 reservation 行重建持有。
UPDATE quota_reservations
SET status = 'reserved',
    request_fingerprint = sqlc.arg(request_fingerprint)::text,
    scope_snapshot = sqlc.arg(scope_snapshot)::jsonb,
    policy_snapshot = sqlc.arg(policy_snapshot)::jsonb,
    predicted_cost = sqlc.arg(predicted_cost)::numeric(20,8),
    reserved_units = sqlc.arg(reserved_units)::numeric(20,8),
    lease_expires_at = sqlc.arg(lease_expires_at)::timestamptz,
    settled_at = NULL,
    released_at = NULL,
    release_reason = NULL,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(reservation_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND status IN ('released', 'expired')
RETURNING
    tenant_id,
    id,
    claim_id,
    request_fingerprint,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    status,
    lease_expires_at,
    created_at,
    updated_at;

-- name: SettleQuotaReservation :execrows
-- Billing commit 后独立事务结算 quota; 失败由 reconciliation job 收敛。
UPDATE quota_reservations
SET status = 'settled',
    settled_cost = sqlc.arg(settled_cost)::numeric(20,8),
    settled_units = sqlc.arg(settled_units)::numeric(20,8),
    overage_units = sqlc.arg(overage_units)::numeric(20,8),
    settled_at = NOW(),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(reservation_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND status IN ('reserved', 'reconciliation_needed');

-- name: ReleaseQuotaReservation :execrows
-- Abort/cache-hit/retry 放弃路径释放 reservation; 调用方同时释放并发槽。
UPDATE quota_reservations
SET status = 'released',
    released_at = NOW(),
    release_reason = sqlc.arg(release_reason)::text,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(reservation_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND status IN ('reserved', 'reconciliation_needed');

-- name: MarkQuotaReservationReconciliationNeeded :execrows
-- 独立 quota settle/release 失败时标记 reservation, 后台 job 幂等重试。
UPDATE quota_reservations
SET status = 'reconciliation_needed',
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(reservation_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND status = 'reserved';

-- name: AcquireQuotaConcurrencySlot :one
-- 本地 scope 并发槽; DB 函数按 tenant/scope 锁行串行化 COUNT+UPSERT。
SELECT
    acquired_slot.tenant_id::bigint AS tenant_id,
    acquired_slot.id::bigint AS id,
    acquired_slot.reservation_id::bigint AS reservation_id,
    acquired_slot.scope_kind::text AS scope_kind,
    acquired_slot.scope_id::text AS scope_id,
    acquired_slot.status::text AS status,
    acquired_slot.lease_expires_at::timestamptz AS lease_expires_at
FROM quota_acquire_concurrency_slot(
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(reservation_id)::bigint,
    sqlc.arg(claim_id)::bigint,
    sqlc.arg(scope_kind)::text,
    sqlc.arg(scope_id)::text,
    sqlc.arg(at_time)::timestamptz,
    sqlc.arg(lease_expires_at)::timestamptz,
    sqlc.arg(slot_limit)::bigint
) AS acquired_slot(
    tenant_id,
    id,
    reservation_id,
    scope_kind,
    scope_id,
    status,
    lease_expires_at
);

-- name: ReleaseQuotaConcurrencySlotsByReservation :execrows
-- 按 reservation 幂等释放所有本地并发槽。
UPDATE quota_concurrency_slots
SET status = 'released',
    released_at = NOW(),
    release_reason = sqlc.arg(release_reason)::text,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND reservation_id = sqlc.arg(reservation_id)::bigint
  AND status = 'acquired';

-- name: ExpireQuotaConcurrencySlots :execrows
-- 租户内 lease 过期清理, 不写 provider cooldown。
UPDATE quota_concurrency_slots qcs
SET status = 'expired',
    released_at = NOW(),
    release_reason = 'lease_expired',
    updated_at = NOW()
WHERE qcs.tenant_id = sqlc.arg(tenant_id)::bigint
  AND qcs.status = 'acquired'
  AND qcs.lease_expires_at <= sqlc.arg(at_time)::timestamptz
  AND NOT EXISTS (
      SELECT 1
      FROM usage_record_dlq d
      WHERE d.tenant_id = qcs.tenant_id
        AND d.claim_id = qcs.claim_id
        AND d.event_kind = 'post_delivery_settlement'
        AND d.status <> 'delivered'
  );

-- name: InsertQuotaAuditEvent :one
-- 配额审计事件; deny/overage/reconcile 都只写 quota audit。
INSERT INTO quota_audit_events (
    tenant_id,
    reservation_id,
    claim_id,
    event_type,
    decision_code,
    scope_kind,
    scope_id,
    metric,
    amount_reserved,
    amount_settled,
    retry_after_seconds,
    payload,
    actor
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.narg(reservation_id)::bigint,
    sqlc.narg(claim_id)::bigint,
    sqlc.arg(event_type)::text,
    sqlc.arg(decision_code)::text,
    sqlc.arg(scope_kind)::text,
    sqlc.arg(scope_id)::text,
    sqlc.arg(metric)::text,
    sqlc.arg(amount_reserved)::numeric(20,8),
    sqlc.arg(amount_settled)::numeric(20,8),
    sqlc.narg(retry_after_seconds)::integer,
    sqlc.arg(payload)::jsonb,
    sqlc.narg(actor)::text
WHERE EXISTS (
    SELECT 1
    FROM tenants t
    WHERE t.id = sqlc.arg(tenant_id)::bigint
)
RETURNING
    tenant_id,
    id,
    occurred_at;

-- name: EnqueueQuotaReconciliationJob :one
-- B1 wrapper 的补偿队列; 同一 claim/kind 的 queued/running job 幂等合并。
INSERT INTO quota_reconciliation_jobs (
    tenant_id,
    claim_id,
    reservation_id,
    job_kind,
    status,
    last_error,
    next_run_at
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(claim_id)::bigint,
    sqlc.narg(reservation_id)::bigint,
    sqlc.arg(job_kind)::text,
    'queued',
    sqlc.narg(last_error)::text,
    sqlc.arg(next_run_at)::timestamptz
WHERE EXISTS (
    SELECT 1
    FROM billing_ledger_claims blc
    WHERE blc.tenant_id = sqlc.arg(tenant_id)::bigint
      AND blc.id = sqlc.arg(claim_id)::bigint
)
ON CONFLICT (tenant_id, claim_id, job_kind)
    WHERE status IN ('queued', 'running')
DO UPDATE SET
    last_error = EXCLUDED.last_error,
    next_run_at = LEAST(quota_reconciliation_jobs.next_run_at, EXCLUDED.next_run_at),
    updated_at = NOW()
WHERE quota_reconciliation_jobs.tenant_id = sqlc.arg(tenant_id)::bigint
RETURNING
    tenant_id,
    id,
    claim_id,
    reservation_id,
    job_kind,
    status,
    attempt_count,
    next_run_at;

-- name: ListDueQuotaReconciliationJobs :many
-- 租户内领取到期 job; 后续切片 B 决定 worker 调度粒度。
SELECT
    tenant_id,
    id,
    claim_id,
    reservation_id,
    job_kind,
    status,
    attempt_count,
    last_error,
    next_run_at
FROM quota_reconciliation_jobs
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND (
      (status IN ('queued', 'failed')
       AND next_run_at <= sqlc.arg(at_time)::timestamptz)
      OR (
          status = 'running'
          AND (
              locked_at IS NULL
              OR locked_at < sqlc.arg(at_time)::timestamptz - INTERVAL '15 minutes'
          )
      )
  )
ORDER BY next_run_at ASC, id ASC
LIMIT sqlc.arg(job_limit)::integer
FOR UPDATE SKIP LOCKED;

-- name: ListTenantsWithDueQuotaReconciliationJobs :many
-- 全局 sweep 入口: 列出「有到期 job」的 distinct 租户, 供跨租户 worker 公平轮转。
-- due 条件与单租户 ListDueQuotaReconciliationJobs 逐字一致(queued/failed 到期, 或 running
-- 但 lock 陈旧), 确保「有 due 的租户」精确; worker 再对每个租户走单租户领取 + 每租户 limit,
-- 天然公平不饿死其它租户。tenant_limit 封顶单轮扫描的租户数。
SELECT DISTINCT tenant_id
FROM quota_reconciliation_jobs
WHERE (
    (status IN ('queued', 'failed')
     AND next_run_at <= sqlc.arg(at_time)::timestamptz)
    OR (
        status = 'running'
        AND (
            locked_at IS NULL
            OR locked_at < sqlc.arg(at_time)::timestamptz - INTERVAL '15 minutes'
        )
    )
)
ORDER BY tenant_id ASC
LIMIT sqlc.arg(tenant_limit)::integer;

-- name: MarkQuotaReconciliationJobRunning :execrows
-- worker 领取 job 后标记 running; 超过 15 分钟的 running 视为可回收 lease。
UPDATE quota_reconciliation_jobs
SET status = 'running',
    locked_at = NOW(),
    attempt_count = attempt_count + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(job_id)::bigint
  AND (
      status IN ('queued', 'failed')
      OR (
          status = 'running'
          AND (
              locked_at IS NULL
              OR locked_at < NOW() - INTERVAL '15 minutes'
          )
      )
  );

-- name: CompleteQuotaReconciliationJob :execrows
-- 补偿成功后终结 job。
UPDATE quota_reconciliation_jobs
SET status = 'succeeded',
    last_error = NULL,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(job_id)::bigint
  AND status = 'running';

-- name: FailQuotaReconciliationJob :execrows
-- 补偿失败后按调用方给出的 next_run_at 重新排队。
UPDATE quota_reconciliation_jobs
SET status = 'failed',
    last_error = sqlc.arg(last_error)::text,
    next_run_at = sqlc.arg(next_run_at)::timestamptz,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(job_id)::bigint
  AND status = 'running';

-- name: ListStaleReservedQuotaReservations :many
-- 清扫入口: lease 过期未终态 + claim 已终态 + 无任何补偿 job 史的孤儿预留(有 job 史的行归 job 重放段, 其退避与终态停靠不可被清扫段每轮重试击穿)。
SELECT qr.tenant_id,
       qr.id AS reservation_id,
       qr.claim_id,
       qr.predicted_cost,
       blc.status AS claim_status,
       blc.actual_cost AS claim_actual_cost
FROM quota_reservations qr
JOIN billing_ledger_claims blc
  ON blc.tenant_id = qr.tenant_id
 AND blc.id = qr.claim_id
WHERE qr.status IN ('reserved', 'reconciliation_needed')
  AND qr.lease_expires_at <= sqlc.arg(at_time)::timestamptz
  AND blc.status IN ('committed', 'aborted')
  AND NOT EXISTS (
      SELECT 1
      FROM quota_reconciliation_jobs j
      WHERE j.tenant_id = qr.tenant_id
        AND j.claim_id = qr.claim_id
        AND j.status IN ('queued', 'running', 'failed')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM usage_record_dlq d
      WHERE d.tenant_id = qr.tenant_id
        AND d.claim_id = qr.claim_id
        AND d.event_kind = 'post_delivery_settlement'
        AND d.status <> 'delivered'
  )
ORDER BY qr.lease_expires_at ASC, qr.id ASC
LIMIT sqlc.arg(row_limit)::integer;

-- name: GetBillingClaimTerminalState :one
-- claim 终态点查: 补偿动作执行前复核 claim 现状(actual_cost 非 NULL 等价于已 commit 写入实结额)。
SELECT status, actual_cost
FROM billing_ledger_claims
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(claim_id)::bigint;
