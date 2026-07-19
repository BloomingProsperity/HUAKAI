-- HUAKAI claim 级配额预留账本。所有读写都显式携带 tenant_id。

-- name: GetQuotaReservationByClaimForUpdate :one
SELECT
    tenant_id,
    id,
    claim_id,
    request_fingerprint,
    requested_model,
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
INSERT INTO quota_reservations (
    tenant_id,
    claim_id,
    request_fingerprint,
    requested_model,
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
    sqlc.arg(requested_model)::text,
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
    requested_model,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    status,
    lease_expires_at,
    created_at,
    updated_at;

-- name: ReactivateQuotaReservation :one
-- released/expired claim 重试通过重新评估后复用原预留行。
UPDATE quota_reservations
SET status = 'reserved',
    request_fingerprint = sqlc.arg(request_fingerprint)::text,
    requested_model = sqlc.arg(requested_model)::text,
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
    requested_model,
    scope_snapshot,
    policy_snapshot,
    predicted_cost,
    reserved_units,
    status,
    lease_expires_at,
    created_at,
    updated_at;

-- name: SettleQuotaReservation :execrows
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
UPDATE quota_reservations
SET status = 'reconciliation_needed',
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(reservation_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND status = 'reserved';

-- name: PrepareQuotaReleaseRecovery :one
-- 仅为仍处于 aborted 终态的 claim 建立释放恢复资格。
UPDATE quota_reservations
SET status = 'reconciliation_needed',
    lease_expires_at = LEAST(lease_expires_at, NOW()),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND claim_id = sqlc.arg(claim_id)::bigint
  AND (sqlc.arg(reservation_id)::bigint = 0 OR id = sqlc.arg(reservation_id)::bigint)
  AND status IN ('reserved', 'reconciliation_needed')
  AND EXISTS (
      SELECT 1
      FROM billing_ledger_claims blc
      WHERE blc.tenant_id = sqlc.arg(tenant_id)::bigint
        AND blc.id = sqlc.arg(claim_id)::bigint
        AND blc.status = 'aborted'
  )
RETURNING id;
