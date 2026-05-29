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
    status
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
    actual_cost, input_cost, output_cost,
    cache_creation_cost, cache_read_cost, image_output_cost,
    end_class, usage_source, confidence_score, pending_reconciliation,
    stream_state, delivered_token_count, stream_terminated_reason,
    drain_outcome, routing_reason, protocol_loss,
    requested_at, upstream_request_at, first_byte_at, first_event_at, last_event_at,
    requested_model, upstream_model, stream, snapshot_version, settlement_source
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7,
    $8, $9,
    $10, $11,
    $12, $13, $14,
    $15, $16, $17,
    $18, $19, $20,
    $21, $22, $23, $24,
    $25, $26, $27,
    $28, $29, $30,
    $31, $32, $33, $34, $35,
    $36, $37, $38, $39, $40
)
RETURNING id;

-- name: SelectPendingReconciliationForFinalize :many
-- Background finalize-after-grace worker: lock aged pending usage rows so
-- concurrent workers do not process the same batch.
SELECT id, tenant_id, claim_id, actual_cost, usage_source
FROM usage_records ur
WHERE ur.pending_reconciliation = true
  -- inferred = usage was inferred (no authoritative upstream usage) -> safe to finalize; reported-but-flagged rows stay pending for genuine reconciliation.
  AND ur.usage_source = 'inferred'
  -- actual_cost = 0 restricts finalize to the no-authoritative-usage streaming provisional (cost was $0).
  -- inferred is ALSO set on UpstreamEOFNoTerminal streams that accumulated PARTIAL usage (cost > 0):
  -- those are abnormal/partial and must stay pending for genuine reconciliation, never auto-finalized.
  AND ur.actual_cost = 0
  AND ur.settled_at < NOW() - sqlc.arg(grace)::interval
  AND NOT EXISTS (
      SELECT 1
      FROM usage_record_reconciliation_events ure
      WHERE ure.tenant_id = ur.tenant_id
        AND ure.original_usage_record_id = ur.id
        AND ure.reconciliation_source = 'finalize_after_grace'
  )
ORDER BY ur.settled_at, ur.id
LIMIT sqlc.arg(batch_size)::int
FOR UPDATE SKIP LOCKED;

-- name: FinalizePendingReconciliation :execrows
-- Append-only finalization marker; rows already finalized by another worker
-- insert 0 rows and are treated as benign.
INSERT INTO usage_record_reconciliation_events (
    tenant_id, original_usage_record_id,
    authoritative_tokens_input, authoritative_tokens_output,
    authoritative_cost, cost_delta, reconciliation_source
)
SELECT
    ur.tenant_id, ur.id,
    -- authoritative tokens = 0: this finalize is for no-authoritative-usage provisionals.
    -- ur.tokens_output may be a delivered CONTENT-FRAME count (not a token count) on the
    -- missing-usage path, so copying it would label frame counts as authoritative usage.
    -- Record 0 authoritative usage to match the $0 settlement (cost_delta = 0, no charge change).
    0, 0,
    ur.actual_cost, 0, 'finalize_after_grace'
FROM usage_records ur
WHERE ur.id = sqlc.arg(id)
  AND ur.tenant_id = sqlc.arg(tenant_id)
  AND ur.pending_reconciliation = true
  -- defensive: only the $0 no-usage provisional is finalize-after-grace eligible (matches the selector).
  AND ur.actual_cost = 0
  AND NOT EXISTS (
      SELECT 1
      FROM usage_record_reconciliation_events ure
      WHERE ure.tenant_id = ur.tenant_id
        AND ure.original_usage_record_id = ur.id
        AND ure.reconciliation_source = 'finalize_after_grace'
  );

-- name: InsertBillingEvent :one
-- Spec §Tx2 step 13: audit-grade event row in same Tx; survives Usage Record
-- async failure (per F-OBS-001 H8). event_type per CHECK constraint.
INSERT INTO billing_events (
    tenant_id, claim_id, event_type,
    actual_cost, actual_cost_signed,
    end_class, usage_source,
    stream_state, delivered_token_count, stream_terminated_reason,
    fingerprint, audit_request_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
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
-- Spec §Tx2 step 14: TRULY IDEMPOTENT in_flight decrement (codex P1 review fix).
-- Atomic CTE: flip pool_slot_acquisitions.status acquired -> released_success
-- AND ONLY THEN decrement provider_accounts.in_flight_count. If the token is
-- replayed (e.g. retry storm), the inner UPDATE returns 0 rows because status
-- is no longer 'acquired'; the outer UPDATE no-ops; in_flight_count stays correct.
-- $2 = release_reason text ('settled_committed' / 'settled_aborted' / etc.)
WITH released AS (
    UPDATE pool_slot_acquisitions
    SET status = 'released_success',
        released_at = NOW(),
        release_reason = $2
    WHERE acquisition_token = $1 AND status = 'acquired'
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
