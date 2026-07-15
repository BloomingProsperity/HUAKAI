-- F-OBS-001 balance hold queries for durable atomic debit.

-- name: ReserveBalanceHold :one
UPDATE user_balances
SET held = held + @cost,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = @tenant_id
  AND user_id = @user_id
  AND (balance - held) >= @cost
RETURNING balance, held, version;

-- name: UpsertBalanceHold :exec
INSERT INTO balance_holds (
    claim_id, tenant_id, user_id, amount, state, resolved_at
) VALUES (
    @claim_id, @tenant_id, @user_id, @amount, 'held', NULL
)
ON CONFLICT (claim_id) DO UPDATE
SET amount = EXCLUDED.amount,
    state = 'held',
    captured = 0,
    resolved_at = NULL
WHERE balance_holds.state = 'released';

-- name: GetBalanceHoldForUpdate :one
SELECT tenant_id, user_id, amount, captured, state
FROM balance_holds
WHERE claim_id = @claim_id
FOR UPDATE;

-- name: GetUserBalance :one
SELECT balance, held
FROM user_balances
WHERE tenant_id = @tenant_id
  AND user_id = @user_id;

-- name: UserBalanceExists :one
-- 区分 ReserveBalanceHold 返 0 行的两种情形:无余额行 vs 行在但
-- (balance-held)<cost。mandatory 由调用方将两者都映射为 402; opt_in 只
-- 对已有余额行不足返回 402。
SELECT EXISTS (
    SELECT 1 FROM user_balances
    WHERE tenant_id = @tenant_id AND user_id = @user_id
) AS exists;

-- name: CaptureBalanceHold :execrows
UPDATE balance_holds
SET state = 'captured',
    captured = @actual,
    resolved_at = NOW()
WHERE claim_id = @claim_id
  AND state = 'held';

-- name: ReleaseBalanceHold :execrows
UPDATE balance_holds
SET state = 'released',
    resolved_at = NOW()
WHERE claim_id = @claim_id
  AND state = 'held';

-- name: ApplyBalanceHoldCapture :one
UPDATE user_balances
SET balance = balance - @actual,
    held = held - @amount,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = @tenant_id
  AND user_id = @user_id
RETURNING balance, held, version;

-- name: ApplyBalanceHoldRelease :one
UPDATE user_balances
SET held = held - @amount,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = @tenant_id
  AND user_id = @user_id
RETURNING balance, held, version;

-- name: SelectExpiredReservingClaims :many
SELECT blc.id, blc.tenant_id
FROM billing_ledger_claims blc
WHERE blc.status = 'reserving'
  AND blc.lease_expires_at < NOW()
  AND NOT EXISTS (
      SELECT 1
      FROM usage_record_dlq d
      WHERE d.tenant_id = blc.tenant_id
        AND d.claim_id = blc.id
        AND d.event_kind = 'post_delivery_settlement'
        AND d.status <> 'delivered'
  )
ORDER BY blc.lease_expires_at
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;

-- name: ExpediteAbortLease :execrows
UPDATE billing_ledger_claims
SET lease_expires_at = LEAST(lease_expires_at, NOW())
WHERE tenant_id = @tenant_id::bigint
  AND id = @claim_id::bigint
  AND status = 'reserving';
