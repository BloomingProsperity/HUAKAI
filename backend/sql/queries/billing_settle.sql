-- F-OBS-001 Tx2 Settler queries.
-- Backed by billing_ledger_claims + usage_records + billing_events tables
-- in docs/schema/observability-billing.sql + scheduler_outbox in
-- docs/schema/pool-routing.sql.

-- name: GetClaimForSettle :one
-- Tx2 entry: lock the claim row + verify still-settle-able state.
-- Spec §Tx2 step 9: re-fetch claim row; verify status='reserving' AND
-- acquisition_token matches.
SELECT
    id,
    tenant_id,
    api_key_id,
    user_id,
    provider_account_id,
    acquisition_token,
    attempt_seq,
    predicted_cost,
    currency_code,
    billing_policy_version,
    requested_model,
    request_fingerprint,
    status,
    billing_effect
FROM billing_ledger_claims
WHERE id = $1 AND tenant_id = $2 AND acquisition_token = $3 AND status = 'reserving'
FOR UPDATE;

-- name: InsertUsageRecord :one
-- Spec §Tx2 step 12: write Usage Record into the same Tx as everything else.
-- Slice 2 (N+5b 2026-05-01): added snapshot_version (column from migration
-- 0008). Format documented there as "registry:<tid>:<v>;router:<rv>".
INSERT INTO usage_records (
    tenant_id, claim_id, api_key_id, user_id, provider_account_id,
    acquisition_token, attempt_seq,
    tokens_input, tokens_output,
    cache_creation_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens, image_output_tokens,
    actual_cost, cost_snapshot, input_cost, output_cost,
    cache_creation_cost, cache_read_cost, image_output_cost,
    end_class, usage_source, confidence_score, pending_reconciliation,
    stream_state, delivered_token_count, stream_terminated_reason,
    drain_outcome, routing_reason, protocol_loss,
    requested_at, upstream_request_at, first_byte_at, first_event_at, last_event_at,
    requested_model, upstream_model, stream, snapshot_version, settlement_source,
    image_count, image_size, image_size_breakdown, ip_address, user_agent,
    client_tool, billing_effect
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7,
    $8, $9,
    $10, $11,
    $12, $13, $14,
    $15, $16, $17, $18,
    $19, $20, $21,
    $22, $23, $24, $25,
    $26, $27, $28,
    $29, $30, $31,
    $32, $33, $34, $35, $36,
    $37, $38, $39, $40, $41,
    $42, $43, $44, $45, $46,
    $47, COALESCE(NULLIF(sqlc.arg(billing_effect)::text, ''), 'user_charge')
)
RETURNING id;

-- name: InsertBillingEvent :one
-- Spec §Tx2 step 13: audit-grade event row in same Tx; survives Usage Record
-- async failure (per F-OBS-001 H8). event_type per CHECK constraint.
INSERT INTO billing_events (
    tenant_id, claim_id, event_type,
    actual_cost, actual_cost_signed,
    end_class, usage_source,
    stream_state, delivered_token_count, stream_terminated_reason,
    fingerprint, audit_request_id, billing_effect
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    COALESCE(NULLIF(sqlc.arg(billing_effect)::text, ''), 'user_charge')
)
RETURNING id, occurred_at;

-- name: InsertSchedulerOutboxRow :one
-- Spec §Tx2 step 11: cross-threshold notification, in same Tx.
-- For Phase B.5 v0.1, the threshold detection itself is stubbed (operator
-- policy returns false unless explicitly forced in test); when the policy
-- returns true, this query enqueues the row.
INSERT INTO scheduler_outbox (
    tenant_id, event_type, pool_group_id, provider_account_id, payload
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING id, created_at;

-- name: ReleaseSlotAndDecrementInFlight :execrows
-- Tx2 槽释放原语：只有 acquired 行成功翻到指定终态后才递减账号在途数。
-- 重放同一 token 时内层 UPDATE 为 0 行，外层自然不递减，保持幂等。
-- 参数使用具名绑定；acquisition_token 由目标列推导 UUID 类型并套用统一类型覆盖。
WITH released AS (
    UPDATE pool_slot_acquisitions
    SET status = sqlc.arg(release_status)::text,
        released_at = NOW(),
        release_reason = sqlc.narg(release_reason)::text
    WHERE acquisition_token = sqlc.arg(acquisition_token) AND status = 'acquired'
    RETURNING provider_account_id
)
UPDATE provider_accounts pa
SET in_flight_count = pa.in_flight_count - 1
FROM released r
WHERE r.provider_account_id = pa.id AND pa.in_flight_count > 0;

-- name: UpdateClaimCommitted :execrows
-- Spec §Tx2 step 15: claim status reserving → committed.
-- codex chunk7 P1#4: tenant_id 显式 caller 提供, 防全局 id 跨租户误 commit。
UPDATE billing_ledger_claims
SET status = 'committed', actual_cost = $2, settled_at = NOW()
WHERE id = $1 AND status = 'reserving' AND tenant_id = $3;

-- name: UpdateClaimAbortedWithReason :execrows
-- Abort path: claim status reserving → aborted; usage_record/billing_event
-- still written (with zero cost) for audit completeness.
-- Tenant-scoped to prevent cross-tenant abort via stale claim id.
UPDATE billing_ledger_claims
SET status = 'aborted', aborted_reason = $3, settled_at = NOW()
WHERE id = $1 AND tenant_id = $2 AND status = 'reserving';
