-- HUAKAI 本地 scope 并发槽。

-- name: AcquireQuotaConcurrencySlot :one
-- 数据库函数按 tenant/scope 锁行，串行化 COUNT 与写入。
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
UPDATE quota_concurrency_slots
SET status = 'released',
    released_at = NOW(),
    release_reason = sqlc.arg(release_reason)::text,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND reservation_id = sqlc.arg(reservation_id)::bigint
  AND status = 'acquired';

-- name: ExpireQuotaConcurrencySlots :execrows
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
