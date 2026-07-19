-- HUAKAI 配额补偿队列、跨租户清扫与 claim 终态复核。

-- name: EnqueueQuotaReconciliationJob :one
-- 同一 claim/kind 的 queued/running job 幂等合并。
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
-- due 条件与单租户领取保持一致，供全局 worker 公平轮转。
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
UPDATE quota_reconciliation_jobs
SET status = 'succeeded',
    last_error = NULL,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(job_id)::bigint
  AND status = 'running';

-- name: FailQuotaReconciliationJob :execrows
UPDATE quota_reconciliation_jobs
SET status = CASE
        WHEN sqlc.arg(terminal)::boolean THEN 'failed'
        ELSE 'queued'
    END,
    last_error = sqlc.arg(last_error)::text,
    next_run_at = sqlc.arg(next_run_at)::timestamptz,
    locked_at = NULL,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(job_id)::bigint
  AND status = 'running';

-- name: ListStaleReservedQuotaReservations :many
-- 找出 lease 过期、claim 已终态且无补偿任务或未投递结算事件的孤儿预留。
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
SELECT status, actual_cost
FROM billing_ledger_claims
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(claim_id)::bigint;
