-- F-OBS-001 admin read APIs. SELECT-only: no hot-path, quota, billing,
-- auth, or trust-chain mutation is allowed here.

-- name: ListUsageRecords :many
SELECT
    ur.id, ur.tenant_id, ur.claim_id, ur.api_key_id, ur.user_id,
    ur.provider_account_id, ur.attempt_seq, ur.tokens_input, ur.tokens_output,
    ur.cache_creation_tokens, ur.cache_read_tokens, ur.actual_cost,
    ur.end_class, ur.usage_source, ur.pending_reconciliation,
    ur.stream_state, ur.delivered_token_count, ur.stream_terminated_reason,
    ur.requested_at, ur.settled_at AS created_at, ur.requested_model,
    ur.upstream_model, ur.stream, ur.settlement_source,
    p.code AS provider, blc.pooling_group_id AS pool_id,
    blc.logical_request_id AS request_id,
    ale.ledger_id AS audit_ledger_id,
    ale.pubkey_fingerprint AS audit_pubkey_fingerprint,
    ale.hop_chain AS audit_hop_chain,
    ale.model_chain AS audit_model_chain,
    ur.ip_address, ur.user_agent, ur.client_tool
FROM usage_records ur
JOIN billing_ledger_claims blc ON blc.id = ur.claim_id AND blc.tenant_id = ur.tenant_id
LEFT JOIN provider_accounts pa ON pa.id = ur.provider_account_id AND pa.tenant_id = ur.tenant_id
LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = ur.tenant_id
LEFT JOIN audit_ledger_entries ale ON ale.request_id = blc.logical_request_id
    AND (ale.tenant_id IS NULL OR ale.tenant_id = ur.tenant_id)
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR ur.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR ur.settled_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR ur.settled_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(provider)::text IS NULL OR p.code = sqlc.narg(provider)::text)
  AND (sqlc.narg(pool_id)::bigint IS NULL OR blc.pooling_group_id = sqlc.narg(pool_id)::bigint)
  AND (sqlc.narg(api_key_id)::bigint IS NULL OR ur.api_key_id = sqlc.narg(api_key_id)::bigint)
  -- user_id 过滤:供会话级用量端点按调用者用户(跨其所有 key)收敛,与 api_key_id 互补。
  AND (sqlc.narg(user_id)::bigint IS NULL OR ur.user_id = sqlc.narg(user_id)::bigint)
  AND (sqlc.narg(provider_account_id)::bigint IS NULL OR ur.provider_account_id = sqlc.narg(provider_account_id)::bigint)
  AND (sqlc.narg(model)::text IS NULL OR ur.requested_model = sqlc.narg(model)::text)
  AND (
    sqlc.arg(pending_reconciliation_only)::boolean = false
    OR (
      ur.pending_reconciliation = true
      AND NOT EXISTS (
        SELECT 1
        FROM usage_record_reconciliation_events re
        WHERE re.tenant_id = ur.tenant_id
          AND re.original_usage_record_id = ur.id
          AND re.reconciliation_source = 'stream_no_usage_finalized'
      )
    )
  )
  AND (
    sqlc.narg(outcome)::text IS NULL
    OR sqlc.narg(outcome)::text = 'all'
    OR (sqlc.narg(outcome)::text = 'success' AND ur.end_class IN ('stream_end_graceful', 'non_streaming'))
    OR (sqlc.narg(outcome)::text = 'error' AND ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))
  )
  AND (sqlc.arg(has_cursor)::boolean = false OR (ur.settled_at, ur.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::bigint))
ORDER BY ur.settled_at DESC, ur.id DESC
LIMIT sqlc.arg(page_limit)::integer;

-- name: ListUsageRecordsWithNames :many
-- Sibling of ListUsageRecords with display names joined for admin/operator UI.
-- Tenant predicates on api_keys/users are deliberate defense-in-depth even
-- though those ids are globally unique today.
SELECT
    ur.id, ur.tenant_id, ur.claim_id, ur.api_key_id, ur.user_id,
    ur.provider_account_id, ur.attempt_seq, ur.tokens_input, ur.tokens_output,
    ur.cache_creation_tokens, ur.cache_read_tokens, ur.actual_cost,
    ur.end_class, ur.usage_source, ur.pending_reconciliation,
    ur.stream_state, ur.delivered_token_count, ur.stream_terminated_reason,
    ur.requested_at, ur.settled_at AS created_at, ur.requested_model,
    ur.upstream_model, ur.stream, ur.settlement_source,
    p.code AS provider, blc.pooling_group_id AS pool_id,
    blc.logical_request_id AS request_id,
    ak.name AS token_name,
    u.display_name AS username,
    ale.ledger_id AS audit_ledger_id,
    ale.pubkey_fingerprint AS audit_pubkey_fingerprint,
    ale.hop_chain AS audit_hop_chain,
    ale.model_chain AS audit_model_chain
FROM usage_records ur
JOIN billing_ledger_claims blc ON blc.id = ur.claim_id AND blc.tenant_id = ur.tenant_id
LEFT JOIN provider_accounts pa ON pa.id = ur.provider_account_id AND pa.tenant_id = ur.tenant_id
LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = ur.tenant_id
LEFT JOIN api_keys ak ON ak.id = ur.api_key_id AND ak.tenant_id = ur.tenant_id
LEFT JOIN users u ON u.id = ur.user_id AND u.tenant_id = ur.tenant_id
LEFT JOIN audit_ledger_entries ale ON ale.request_id = blc.logical_request_id
    AND (ale.tenant_id IS NULL OR ale.tenant_id = ur.tenant_id)
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR ur.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR ur.settled_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR ur.settled_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(provider)::text IS NULL OR p.code = sqlc.narg(provider)::text)
  AND (sqlc.narg(pool_id)::bigint IS NULL OR blc.pooling_group_id = sqlc.narg(pool_id)::bigint)
  AND (sqlc.narg(api_key_id)::bigint IS NULL OR ur.api_key_id = sqlc.narg(api_key_id)::bigint)
  AND (sqlc.narg(provider_account_id)::bigint IS NULL OR ur.provider_account_id = sqlc.narg(provider_account_id)::bigint)
  AND (sqlc.narg(model)::text IS NULL OR ur.requested_model = sqlc.narg(model)::text)
  AND (
    sqlc.arg(pending_reconciliation_only)::boolean = false
    OR (
      ur.pending_reconciliation = true
      AND NOT EXISTS (
        SELECT 1
        FROM usage_record_reconciliation_events re
        WHERE re.tenant_id = ur.tenant_id
          AND re.original_usage_record_id = ur.id
          AND re.reconciliation_source = 'stream_no_usage_finalized'
      )
    )
  )
  AND (
    sqlc.narg(outcome)::text IS NULL
    OR sqlc.narg(outcome)::text = 'all'
    OR (sqlc.narg(outcome)::text = 'success' AND ur.end_class IN ('stream_end_graceful', 'non_streaming'))
    OR (sqlc.narg(outcome)::text = 'error' AND ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))
  )
  AND (sqlc.arg(has_cursor)::boolean = false OR (ur.settled_at, ur.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::bigint))
ORDER BY ur.settled_at DESC, ur.id DESC
LIMIT sqlc.arg(page_limit)::integer;

-- name: GetUsageRecordByRequestID :one
WITH scoped_usage_records AS (
    SELECT
        ur.id, ur.tenant_id, ur.claim_id, ur.api_key_id, ur.user_id,
        ur.provider_account_id, ur.attempt_seq, ur.tokens_input, ur.tokens_output,
        ur.cache_creation_tokens, ur.cache_read_tokens, ur.actual_cost,
        ur.end_class, ur.usage_source, ur.pending_reconciliation,
        ur.stream_state, ur.delivered_token_count, ur.stream_terminated_reason,
        ur.requested_at, ur.settled_at AS created_at, ur.requested_model,
        ur.upstream_model, ur.stream, ur.settlement_source,
        p.code AS provider, blc.pooling_group_id AS pool_id,
        blc.logical_request_id AS request_id,
        ale.ledger_id AS audit_ledger_id,
        ale.pubkey_fingerprint AS audit_pubkey_fingerprint,
        ale.hop_chain AS audit_hop_chain,
        ale.model_chain AS audit_model_chain,
        row_number() OVER (ORDER BY ur.settled_at DESC, ur.id DESC) AS rn
    FROM usage_records ur
    JOIN billing_ledger_claims blc ON blc.id = ur.claim_id AND blc.tenant_id = ur.tenant_id
    LEFT JOIN provider_accounts pa ON pa.id = ur.provider_account_id AND pa.tenant_id = ur.tenant_id
    LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = ur.tenant_id
    LEFT JOIN audit_ledger_entries ale ON ale.request_id = blc.logical_request_id
        AND (ale.tenant_id IS NULL OR ale.tenant_id = ur.tenant_id)
    WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
      AND ur.user_id = sqlc.arg(user_id)::bigint
      AND ur.api_key_id = sqlc.arg(api_key_id)::bigint
      AND blc.logical_request_id = sqlc.arg(request_id)::text
),
request_totals AS (
    SELECT
        sum(tokens_input)::integer AS tokens_input,
        sum(tokens_output)::integer AS tokens_output,
        sum(cache_creation_tokens)::integer AS cache_creation_tokens,
        sum(cache_read_tokens)::integer AS cache_read_tokens,
        sum(actual_cost)::numeric AS actual_cost,
        sum(delivered_token_count)::bigint AS delivered_token_count
    FROM scoped_usage_records
)
SELECT
    s.id, s.tenant_id, s.claim_id, s.api_key_id, s.user_id,
    s.provider_account_id, s.attempt_seq,
    t.tokens_input, t.tokens_output, t.cache_creation_tokens, t.cache_read_tokens,
    t.actual_cost,
    s.end_class, s.usage_source, s.pending_reconciliation,
    s.stream_state, t.delivered_token_count, s.stream_terminated_reason,
    s.requested_at, s.created_at, s.requested_model,
    s.upstream_model, s.stream, s.settlement_source,
    s.provider, s.pool_id, s.request_id,
    s.audit_ledger_id, s.audit_pubkey_fingerprint,
    s.audit_hop_chain, s.audit_model_chain
FROM scoped_usage_records s
CROSS JOIN request_totals t
WHERE s.rn = 1;

-- name: CountUsageRecords :one
SELECT count(*)::bigint
FROM usage_records ur
JOIN billing_ledger_claims blc ON blc.id = ur.claim_id AND blc.tenant_id = ur.tenant_id
LEFT JOIN provider_accounts pa ON pa.id = ur.provider_account_id AND pa.tenant_id = ur.tenant_id
LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = ur.tenant_id
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR ur.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR ur.settled_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR ur.settled_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(provider)::text IS NULL OR p.code = sqlc.narg(provider)::text)
  AND (sqlc.narg(pool_id)::bigint IS NULL OR blc.pooling_group_id = sqlc.narg(pool_id)::bigint)
  AND (sqlc.narg(api_key_id)::bigint IS NULL OR ur.api_key_id = sqlc.narg(api_key_id)::bigint)
  AND (sqlc.narg(provider_account_id)::bigint IS NULL OR ur.provider_account_id = sqlc.narg(provider_account_id)::bigint)
  AND (sqlc.narg(model)::text IS NULL OR ur.requested_model = sqlc.narg(model)::text)
  AND (
    sqlc.arg(pending_reconciliation_only)::boolean = false
    OR (
      ur.pending_reconciliation = true
      AND NOT EXISTS (
        SELECT 1
        FROM usage_record_reconciliation_events re
        WHERE re.tenant_id = ur.tenant_id
          AND re.original_usage_record_id = ur.id
          AND re.reconciliation_source = 'stream_no_usage_finalized'
      )
    )
  )
  AND (
    sqlc.narg(outcome)::text IS NULL
    OR sqlc.narg(outcome)::text = 'all'
    OR (sqlc.narg(outcome)::text = 'success' AND ur.end_class IN ('stream_end_graceful', 'non_streaming'))
    OR (sqlc.narg(outcome)::text = 'error' AND ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))
  );

-- name: CountPendingReconciliationUsageRecords :one
SELECT count(*)::bigint
FROM usage_records ur
WHERE ur.pending_reconciliation = true
  AND NOT EXISTS (
    SELECT 1
    FROM usage_record_reconciliation_events re
    WHERE re.tenant_id = ur.tenant_id
      AND re.original_usage_record_id = ur.id
      AND re.reconciliation_source = 'stream_no_usage_finalized'
  );

-- name: ListBillingClaims :many
SELECT
    blc.id, blc.tenant_id, blc.idempotency_key, blc.api_key_id, blc.user_id,
    blc.logical_request_id, blc.endpoint_family, blc.requested_model,
    blc.pooling_group_id AS pool_id, blc.provider_account_id, blc.attempt_seq,
    blc.predicted_cost, blc.actual_cost, blc.currency_code, blc.status,
    blc.aborted_reason, blc.reserved_at AS created_at, blc.settled_at, p.code AS provider
FROM billing_ledger_claims blc
LEFT JOIN provider_accounts pa ON pa.id = blc.provider_account_id AND pa.tenant_id = blc.tenant_id
LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = blc.tenant_id
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR blc.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR blc.reserved_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR blc.reserved_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(status)::text IS NULL OR blc.status = sqlc.narg(status)::text)
  AND (sqlc.narg(provider)::text IS NULL OR p.code = sqlc.narg(provider)::text)
  AND (sqlc.narg(pool_id)::bigint IS NULL OR blc.pooling_group_id = sqlc.narg(pool_id)::bigint)
  AND (sqlc.narg(api_key_id)::bigint IS NULL OR blc.api_key_id = sqlc.narg(api_key_id)::bigint)
  AND (sqlc.narg(provider_account_id)::bigint IS NULL OR blc.provider_account_id = sqlc.narg(provider_account_id)::bigint)
  AND (sqlc.narg(model)::text IS NULL OR blc.requested_model = sqlc.narg(model)::text)
  AND (sqlc.arg(has_cursor)::boolean = false OR (blc.reserved_at, blc.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::bigint))
ORDER BY blc.reserved_at DESC, blc.id DESC
LIMIT sqlc.arg(page_limit)::integer;

-- name: CountBillingClaims :one
SELECT count(*)::bigint
FROM billing_ledger_claims blc
LEFT JOIN provider_accounts pa ON pa.id = blc.provider_account_id AND pa.tenant_id = blc.tenant_id
LEFT JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = blc.tenant_id
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR blc.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR blc.reserved_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR blc.reserved_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(status)::text IS NULL OR blc.status = sqlc.narg(status)::text)
  AND (sqlc.narg(provider)::text IS NULL OR p.code = sqlc.narg(provider)::text)
  AND (sqlc.narg(pool_id)::bigint IS NULL OR blc.pooling_group_id = sqlc.narg(pool_id)::bigint)
  AND (sqlc.narg(api_key_id)::bigint IS NULL OR blc.api_key_id = sqlc.narg(api_key_id)::bigint)
  AND (sqlc.narg(provider_account_id)::bigint IS NULL OR blc.provider_account_id = sqlc.narg(provider_account_id)::bigint)
  AND (sqlc.narg(model)::text IS NULL OR blc.requested_model = sqlc.narg(model)::text);

-- name: ListAuditEvents :many
WITH audit_union AS (
    SELECT be.id, be.tenant_id, 'billing'::text AS event_class, be.event_type,
           CASE WHEN be.event_type = 'claim_aborted' THEN 'warning' ELSE 'info' END AS severity,
           be.claim_id::text AS ledger_id, be.claim_id, NULL::bigint AS provider_account_id,
           NULL::bigint AS pool_group_id, NULL::text AS request_id, NULL::text AS actor_id,
           NULL::text AS actor_role, NULL::text AS reason,
           jsonb_build_object('actual_cost', be.actual_cost::text, 'actual_cost_signed', be.actual_cost_signed::text,
                              'end_class', be.end_class, 'usage_source', be.usage_source,
                              'stream_state', be.stream_state,
                              'delivered_token_count', be.delivered_token_count,
                              'stream_terminated_reason', be.stream_terminated_reason,
                              'fingerprint', be.fingerprint) AS payload,
           be.occurred_at AS created_at
    FROM billing_events be
    UNION ALL
    SELECT pe.id, pe.tenant_id, 'pool_routing'::text, pe.event_type,
           CASE WHEN pe.event_type IN ('forced_route_authorization_failed','pool_exhausted','sticky_binding_broken') THEN 'warning' ELSE 'info' END,
           COALESCE(pe.request_id, ''), 0::bigint, pe.provider_account_id, pe.pool_group_id, pe.request_id,
           pe.actor_id, pe.actor_role, pe.reason, pe.payload, pe.created_at
    FROM pool_routing_audit_events pe
    UNION ALL
    SELECT re.id, re.tenant_id, 'rate_limit'::text, re.event_type,
           CASE WHEN re.event_type IN ('permanent_disable_set','refresh_attempt_exhausted_disable') THEN 'error'
                WHEN re.event_type LIKE '%_set' OR re.event_type = 'openai_403_counter_increment' THEN 'warning' ELSE 'info' END,
           COALESCE(re.upstream_request_id, ''), 0::bigint, re.provider_account_id, NULL::bigint, re.upstream_request_id,
           re.actor_id, NULL::text, NULL::text, re.payload, re.occurred_at
    FROM rate_limit_audit_events re
    UNION ALL
    SELECT oe.id, oe.tenant_id, 'oauth_refresh'::text, oe.outcome,
           CASE WHEN oe.outcome IN ('storm_budget_exhausted','token_malformed','permanent_disable','auth_expired','risk_control_triggered','account_disabled') THEN 'error'
                WHEN oe.outcome = 'rate_limit_exceeded' THEN 'warning'
                WHEN oe.outcome IN ('db_version_conflict','invalid_grant_race_recovered','cas_lost') THEN 'warning' ELSE 'info' END,
           COALESCE(oe.request_id, ''), 0::bigint, oe.provider_account_id, NULL::bigint, oe.request_id,
           NULL::text, NULL::text, NULL::text,
           jsonb_build_object('storm_scope', oe.storm_scope, 'client_protocol', oe.client_protocol,
                              'model', oe.model, 'error_class', oe.error_class,
                              'error_message_redacted', oe.error_message_redacted,
                              'mimicry_policy_version', oe.mimicry_policy_version) AS payload,
           oe.occurred_at
    FROM oauth_refresh_audit_events oe
)
SELECT *
FROM audit_union au
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR au.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR au.created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR au.created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(event_class)::text IS NULL OR au.event_class = sqlc.narg(event_class)::text)
  AND (sqlc.narg(event_type)::text IS NULL OR au.event_type = sqlc.narg(event_type)::text)
  AND (sqlc.narg(severity)::text IS NULL OR au.severity = sqlc.narg(severity)::text)
  AND (sqlc.narg(ledger_id)::text IS NULL OR au.ledger_id = sqlc.narg(ledger_id)::text)
  AND (sqlc.narg(actor_id)::text IS NULL OR au.actor_id = sqlc.narg(actor_id)::text)
  AND (sqlc.arg(has_cursor)::boolean = false OR (au.created_at, au.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::bigint))
ORDER BY au.created_at DESC, au.id DESC
LIMIT sqlc.arg(page_limit)::integer;

-- name: CountAuditEvents :one
WITH audit_union AS (
    SELECT id, tenant_id, 'billing'::text AS event_class, event_type,
           CASE WHEN event_type = 'claim_aborted' THEN 'warning' ELSE 'info' END AS severity,
           claim_id::text AS ledger_id, NULL::text AS actor_id, occurred_at AS created_at FROM billing_events
    UNION ALL
    SELECT id, tenant_id, 'pool_routing'::text, event_type,
           CASE WHEN event_type IN ('forced_route_authorization_failed','pool_exhausted','sticky_binding_broken') THEN 'warning' ELSE 'info' END,
           COALESCE(request_id, ''), actor_id, created_at FROM pool_routing_audit_events
    UNION ALL
    SELECT id, tenant_id, 'rate_limit'::text, event_type,
           CASE WHEN event_type IN ('permanent_disable_set','refresh_attempt_exhausted_disable') THEN 'error'
                WHEN event_type LIKE '%_set' OR event_type = 'openai_403_counter_increment' THEN 'warning' ELSE 'info' END,
           COALESCE(upstream_request_id, ''), actor_id, occurred_at FROM rate_limit_audit_events
    UNION ALL
    SELECT id, tenant_id, 'oauth_refresh'::text, outcome,
           CASE WHEN outcome IN ('storm_budget_exhausted','token_malformed','permanent_disable','auth_expired','risk_control_triggered','account_disabled') THEN 'error'
                WHEN outcome = 'rate_limit_exceeded' THEN 'warning'
                WHEN outcome IN ('db_version_conflict','invalid_grant_race_recovered','cas_lost') THEN 'warning' ELSE 'info' END,
           COALESCE(request_id, ''), NULL::text, occurred_at FROM oauth_refresh_audit_events
)
SELECT count(*)::bigint
FROM audit_union au
WHERE (sqlc.narg(tenant_id)::bigint IS NULL OR au.tenant_id = sqlc.narg(tenant_id)::bigint)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR au.created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR au.created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(event_class)::text IS NULL OR au.event_class = sqlc.narg(event_class)::text)
  AND (sqlc.narg(event_type)::text IS NULL OR au.event_type = sqlc.narg(event_type)::text)
  AND (sqlc.narg(severity)::text IS NULL OR au.severity = sqlc.narg(severity)::text)
  AND (sqlc.narg(ledger_id)::text IS NULL OR au.ledger_id = sqlc.narg(ledger_id)::text)
  AND (sqlc.narg(actor_id)::text IS NULL OR au.actor_id = sqlc.narg(actor_id)::text);

-- name: PurgeUsageRecordsBefore :one
-- Usage-log retention only. Deletes bounded batches from usage_records and
-- deliberately does not touch billing_ledger_claims, billing_events, audit
-- tables, or other money/trust-chain records.
WITH doomed AS (
    SELECT id
    FROM usage_records
    WHERE settled_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY settled_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)::integer
    FOR UPDATE SKIP LOCKED
),
deleted AS (
    DELETE FROM usage_records ur
    USING doomed
    WHERE ur.id = doomed.id
    RETURNING ur.id
)
SELECT count(*)::bigint FROM deleted;
