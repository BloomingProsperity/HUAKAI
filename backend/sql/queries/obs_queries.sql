-- F-OBS-001 read-only query surface for the admin/audit lane.
-- Per docs/specs/_invariants/cross-module-boundaries.md CMB-7: this file
-- contains SELECT-only queries; the Repository wrapper enforces tenant
-- scope on every call.
--
-- Per CMB-5: NONE of these SELECTs include the `credentials` column from
-- provider_accounts (or any synonym). Audit views surface metadata only.

-- name: ListUsageByTenant :many
-- Page through usage_records for one tenant. Most-recent-first.
SELECT
    id,
    tenant_id,
    claim_id,
    api_key_id,
    user_id,
    provider_account_id,
    attempt_seq,
    tokens_input,
    tokens_output,
    cache_creation_tokens,
    cache_read_tokens,
    actual_cost,
    end_class,
    usage_source,
    pending_reconciliation,
    stream_state,
    delivered_token_count,
    stream_terminated_reason,
    requested_model,
    upstream_model,
    requested_at,
    settled_at,
    stream
FROM usage_records
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY requested_at DESC, id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);

-- name: GetClaimByID :one
-- Single claim lookup, tenant-scoped (refuse cross-tenant reads).
SELECT
    id,
    tenant_id,
    api_key_id,
    user_id,
    logical_request_id,
    endpoint_family,
    requested_model,
    pooling_group_id,
    billing_policy_version,
    request_class,
    provider_account_id,
    attempt_seq,
    predicted_cost,
    actual_cost,
    currency_code,
    status,
    aborted_reason,
    request_fingerprint,
    reserved_at,
    settled_at
FROM billing_ledger_claims
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);

-- name: ListBillingEventsByTenant :many
-- Audit-grade event stream for one tenant. event_type filter optional;
-- pass empty string to disable filter.
SELECT
    id,
    tenant_id,
    claim_id,
    event_type,
    actual_cost,
    actual_cost_signed,
    end_class,
    usage_source,
    stream_state,
    delivered_token_count,
    stream_terminated_reason,
    fingerprint,
    occurred_at
FROM billing_events
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(event_type_filter)::text = '' OR event_type = sqlc.arg(event_type_filter))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);

-- name: CountClaimsByStatus :many
-- Operator overview: how many claims are in each status for one tenant.
SELECT
    status,
    count(*) AS claim_count
FROM billing_ledger_claims
WHERE tenant_id = sqlc.arg(tenant_id)
GROUP BY status
ORDER BY status;
