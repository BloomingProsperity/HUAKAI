-- F-OBS-001 Tx1/Tx2 billing ledger claim queries.
-- Backed by billing_ledger_claims in docs/schema/observability-billing.sql.

-- name: GetClaimByIdempotency :one
-- Hot-path Tx1 lookup with FOR UPDATE row lock per spec §Tx1 step 2.
-- Selects ONLY the columns the gate needs to make a control-flow decision;
-- nullable money/token fields (actual_cost, acquisition_token) are populated
-- in Tx2 and would otherwise force NullDecimal/pgtype.UUID round-trips on
-- every replay lookup. Other audit fields are queried separately when needed.
SELECT
    id,
    request_fingerprint,
    status,
    attempt_seq,
    billing_effect
FROM billing_ledger_claims
WHERE tenant_id = $1 AND api_key_id = $2 AND idempotency_key = $3
FOR UPDATE;

-- name: GetClaimFingerprintByLogicalRequestID :many
-- Replay-attack detection: same logical_request_id with different fingerprint
-- means an attacker reused the request id across different payloads. Spec §Tx1 step 3
-- third bullet → return FINGERPRINT_CONFLICT (409). Hash differs so the unique
-- idempotency index does NOT catch this; we scan by logical_request_id.
SELECT id, request_fingerprint, status
FROM billing_ledger_claims
WHERE tenant_id = $1 AND api_key_id = $2 AND logical_request_id = $3;

-- name: InsertClaim :one
-- Insert a new reserving claim. Caller MUST hold the row lock acquired via
-- GetClaimByIdempotency (which returns 0 rows when no prior claim exists),
-- so this insert is conflict-free under serializable isolation.
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint,
    api_key_id, user_id, logical_request_id, endpoint_family,
    requested_model, pooling_group_id, billing_policy_version, request_class,
    predicted_cost, currency_code, lease_expires_at, billing_effect
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    COALESCE(NULLIF(sqlc.arg(billing_effect)::text, ''), 'user_charge')
)
RETURNING id, status, reserved_at, attempt_seq;

-- name: WriteAcquisitionToken :execrows
-- Pattern B placeholder writeback per F-POOL-001 §6 + F-OBS-001 §Tx1 step 6.
-- Pool acquire returns; we set provider_account_id + acquisition_token onto
-- the existing reserving claim row.
UPDATE billing_ledger_claims
SET provider_account_id = $2, acquisition_token = $3
WHERE id = $1 AND status = 'reserving' AND tenant_id = $4;

-- name: AbortClaim :execrows
-- Tx2 abort path: terminal upstream failure or AMBIGUOUS_USAGE end class.
-- codex chunk7 P1#4: tenant_id 必须显式预先 caller 提供, 防全局 id 跨租户误改。
UPDATE billing_ledger_claims
SET status = 'aborted', aborted_reason = $2, settled_at = NOW()
WHERE id = $1 AND status = 'reserving' AND tenant_id = $3;

-- name: ReReserveAbortedClaim :one
-- Re-attempt path: an earlier attempt aborted (transient upstream failure,
-- not FINGERPRINT_CONFLICT). Operator policy allows resurrecting the row
-- under the same idempotency_key rather than inserting a duplicate (which
-- would violate uq_claims_idempotency). attempt_seq increments so audits
-- can count retries. Returns the row's id and bumped attempt_seq.
UPDATE billing_ledger_claims
SET status = 'reserving',
    aborted_reason = NULL,
    settled_at = NULL,
    attempt_seq = attempt_seq + 1,
    lease_expires_at = $2,
    predicted_cost = $3,
    pooling_group_id = $4,
    provider_account_id = NULL,
    acquisition_token = NULL,
    reserved_at = NOW()
WHERE id = $1 AND status = 'aborted' AND tenant_id = $5
RETURNING id, attempt_seq;
