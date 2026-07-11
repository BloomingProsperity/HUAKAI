-- 结算意图在首个业务字节前写入。balance_holds 以 claim_id 为主键，
-- 因此这里只在真实 hold 行存在时记录该稳定标识；未启用余额强制的请求保持 NULL。
-- name: InsertSettlementIntent :one
INSERT INTO settlement_intents (
    tenant_id,
    request_id,
    logical_request_id,
    attempt_seq,
    claim_id,
    api_key_id,
    request_fingerprint,
    predicted_cost,
    hold_id
) VALUES (
    @tenant_id,
    @request_id,
    @logical_request_id,
    @attempt_seq,
    @claim_id,
    @api_key_id,
    @request_fingerprint,
    @predicted_cost,
    (SELECT claim_id FROM balance_holds WHERE claim_id = @claim_id)
)
RETURNING id;

-- name: MarkSettlementIntentDelivering :one
UPDATE settlement_intents
SET status = 'delivering',
    first_byte_at = @first_byte_at,
    updated_at = NOW(),
    version = version + 1
WHERE id = @id
  AND version = @version
RETURNING version;

-- name: MarkSettlementIntentSettling :one
UPDATE settlement_intents
SET status = 'settling',
    actual_cost = @actual_cost,
    updated_at = NOW(),
    version = version + 1
WHERE id = @id
  AND version = @version
RETURNING version;

-- name: MarkSettlementIntentSettled :one
UPDATE settlement_intents
SET status = 'settled',
    actual_cost = @actual_cost,
    settled_at = @settled_at,
    updated_at = NOW(),
    version = version + 1
WHERE id = @id
  AND version = @version
RETURNING version;

-- name: MarkSettlementIntentAborted :one
UPDATE settlement_intents
SET status = 'aborted',
    updated_at = NOW(),
    version = version + 1
WHERE id = @id
  AND version = @version
RETURNING version;

-- name: MarkSettlementIntentFailed :one
UPDATE settlement_intents
SET status = 'failed',
    actual_cost = @actual_cost,
    updated_at = NOW(),
    version = version + 1
WHERE id = @id
  AND version = @version
RETURNING version;

-- name: GetSettlementIntentByClaimAttempt :one
SELECT *
FROM settlement_intents
WHERE tenant_id = @tenant_id
  AND claim_id = @claim_id
  AND attempt_seq = @attempt_seq;

-- name: CountUnresolvedSettlementIntentsForClaim :one
SELECT COUNT(*)
FROM settlement_intents
WHERE tenant_id = @tenant_id
  AND claim_id = @claim_id
  AND status NOT IN ('settled', 'aborted', 'superseded');
