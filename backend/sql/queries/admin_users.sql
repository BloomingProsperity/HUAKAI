-- Admin user read-only queries.
-- Tenant predicates are mandatory on every query. These queries never return
-- password hashes, API key hashes, or other credential material.

-- name: AdminListUsersForTenant :many
SELECT
    u.id,
    COALESCE(u.email, '')::text AS email,
    u.role,
    u.status,
    u.user_group,
    u.remark,
    COALESCE(ub.balance, 0)::numeric(20,8)::text AS balance,
    u.created_at
FROM users u
LEFT JOIN user_balances ub
  ON ub.tenant_id = u.tenant_id
 AND ub.user_id = u.id
WHERE u.tenant_id = sqlc.arg(tenant_id)::bigint
  AND u.deleted_at IS NULL
  AND (
    sqlc.arg(query)::text = ''
    OR u.email ILIKE '%' || sqlc.arg(query)::text || '%'
    OR u.display_name ILIKE '%' || sqlc.arg(query)::text || '%'
  )
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: AdminGetUserForTenant :one
SELECT
    u.id,
    COALESCE(u.email, '')::text AS email,
    u.role,
    u.status,
    u.user_group,
    u.remark,
    COALESCE(ub.balance, 0)::numeric(20,8)::text AS balance,
    u.created_at
FROM users u
LEFT JOIN user_balances ub
  ON ub.tenant_id = u.tenant_id
 AND ub.user_id = u.id
WHERE u.tenant_id = sqlc.arg(tenant_id)::bigint
  AND u.id = sqlc.arg(user_id)::bigint
  AND u.deleted_at IS NULL;

-- name: AdminGetTwoFAAdoptionStatsForTenant :one
WITH enabled AS (
    SELECT COUNT(*)::bigint AS enabled_count
    FROM two_factor_settings
    WHERE tenant_id = sqlc.arg(tenant_id)::bigint
      AND is_enabled = true
),
total_users AS (
    SELECT COUNT(*)::bigint AS total_user_count
    FROM users
    WHERE tenant_id = sqlc.arg(tenant_id)::bigint
      AND deleted_at IS NULL
)
SELECT
    enabled.enabled_count,
    total_users.total_user_count
FROM enabled
CROSS JOIN total_users;

-- name: AdminListUserBalanceHistoryForTenant :many
SELECT
    be.id,
    be.event_type,
    be.actual_cost_signed::numeric(20,8)::text AS amount,
    be.fingerprint,
    CASE
        WHEN be.payment_credit_id IS NOT NULL THEN 'payment_credit'
        WHEN be.payment_refund_id IS NOT NULL THEN 'payment_refund'
        WHEN be.voucher_redemption_id IS NOT NULL THEN 'voucher_redemption'
        WHEN be.recharge_order_id IS NOT NULL THEN 'recharge_order'
        WHEN be.subscription_auto_renewal_charge_id IS NOT NULL THEN 'subscription_auto_renewal'
        WHEN be.claim_id IS NOT NULL THEN 'billing_claim'
        ELSE 'billing_event'
    END::text AS source_type,
    COALESCE(
        be.payment_credit_id,
        be.payment_refund_id,
        be.voucher_redemption_id,
        be.recharge_order_id,
        be.subscription_auto_renewal_charge_id,
        be.claim_id,
        be.id
    )::bigint AS source_id,
    be.occurred_at
FROM billing_events be
LEFT JOIN billing_ledger_claims blc
  ON blc.tenant_id = be.tenant_id
 AND blc.id = be.claim_id
LEFT JOIN voucher_redemption vr
  ON vr.tenant_id = be.tenant_id
 AND vr.id = be.voucher_redemption_id
LEFT JOIN payment_credits pc
  ON pc.tenant_id = be.tenant_id
 AND pc.id = be.payment_credit_id
LEFT JOIN payment_refunds pr
  ON pr.tenant_id = be.tenant_id
 AND pr.id = be.payment_refund_id
LEFT JOIN recharge_orders ro
  ON ro.tenant_id = be.tenant_id
 AND ro.id = be.recharge_order_id
LEFT JOIN subscription_auto_renewal_charges sarc
  ON sarc.tenant_id = be.tenant_id
 AND sarc.id = be.subscription_auto_renewal_charge_id
WHERE be.tenant_id = sqlc.arg(tenant_id)::bigint
  AND COALESCE(blc.user_id, vr.user_id, pc.user_id, pr.user_id, ro.user_id, sarc.user_id) = sqlc.arg(user_id)::bigint
ORDER BY be.occurred_at DESC, be.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;
