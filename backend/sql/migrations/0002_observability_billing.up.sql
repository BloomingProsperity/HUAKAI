-- HUAKAI 观测与计费基础结构
-- ============================================================================
-- 提供 F-OBS-001 与 F-BILL-001 所需表面。
-- DR-001: every primary table carries non-null tenant_id.
-- DR-006: PostgreSQL with sqlc; SERIALIZABLE isolation for Tx1/Tx2 explicit.
-- F-OBS-001 invariant: numeric(20,8) money types end-to-end; immutable
-- Usage Record + Billing Ledger entry; transactional outbox for cache
-- invalidation; DLQ for Usage Record async failures.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- Table: billing_ledger_claims
-- ----------------------------------------------------------------------------
-- Idempotent claim row (Tx1 reservation entry; Tx2 settles to committed).
-- Money-grade core: this is the source of truth for "did this request bill?".
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_ledger_claims (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    -- Idempotency key (HASH of: tenant_id, api_key_id, logical_request_id,
    --   endpoint_family, normalized_payload_hash, requested_model,
    --   pooling_group_id, billing_policy_version, request_class)
    idempotency_key             text        NOT NULL,
    -- Fingerprint = the same hash; we keep both for audit + future-proofing
    request_fingerprint         text        NOT NULL,
    -- Request identity
    api_key_id                  bigint      NOT NULL,
    user_id                     bigint      NOT NULL,
    logical_request_id          text        NOT NULL,
    endpoint_family             text        NOT NULL,    -- 'chat', 'responses', 'messages', 'embeddings'
    requested_model             text        NOT NULL,
    pooling_group_id            bigint,                  -- FK to pool_groups (added once F-POOL-001 schema joined)
    billing_policy_version      text        NOT NULL,
    request_class               text        NOT NULL,    -- 'standard', 'priority', 'batch'
    -- Provider Account (Pattern B placeholder; written by F-POOL-001 Phase C)
    provider_account_id         bigint,                  -- NULL until Pool acquire
    acquisition_token           uuid,                    -- written back by Pool acquire
    attempt_seq                 integer     NOT NULL DEFAULT 1,
    -- Money fields (numeric(20,8) per F-OBS-001 H4)
    predicted_cost              numeric(20,8) NOT NULL DEFAULT 0,
    actual_cost                 numeric(20,8),           -- NULL until Tx2
    currency_code               char(3)     NOT NULL DEFAULT 'USD',
    -- Lifecycle status
    status                      text        NOT NULL DEFAULT 'reserving'
                                    CHECK (status IN ('reserving', 'committed', 'aborted')),
    aborted_reason              text,                    -- when status='aborted'
    -- Audit timestamps
    reserved_at                 timestamptz NOT NULL DEFAULT now(),
    settled_at                  timestamptz,
    -- Lease (orphan sweep)
    lease_expires_at            timestamptz NOT NULL
);
CREATE UNIQUE INDEX uq_claims_idempotency
    ON billing_ledger_claims (tenant_id, api_key_id, idempotency_key);
CREATE INDEX idx_claims_status_lease
    ON billing_ledger_claims (status, lease_expires_at)
    WHERE status = 'reserving';
CREATE INDEX idx_claims_user_settled
    ON billing_ledger_claims (user_id, settled_at DESC);
CREATE INDEX idx_claims_account_settled
    ON billing_ledger_claims (provider_account_id, settled_at DESC)
    WHERE provider_account_id IS NOT NULL;
COMMENT ON TABLE billing_ledger_claims IS 'F-OBS-001 Tx1/Tx2 claim row. Money-grade source of truth. Once status=committed, immutable except for downstream paired adjustments.';
COMMENT ON COLUMN billing_ledger_claims.provider_account_id IS 'Pattern B placeholder per F-POOL-001 §6. NULL during reserving; populated by Pool acquire writeback in same Tx1 commit.';

-- ----------------------------------------------------------------------------
-- Table: billing_ledger_archive
-- ----------------------------------------------------------------------------
-- Archive of retired claims (post-retention-period cold storage).
-- Replay attempt on archived idempotency_key with mismatched fingerprint
-- returns ErrUsageBillingRequestConflict.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_ledger_archive (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    idempotency_key             text        NOT NULL,
    request_fingerprint         text        NOT NULL,
    api_key_id                  bigint      NOT NULL,
    archived_at                 timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_archive_idempotency
    ON billing_ledger_archive (tenant_id, api_key_id, idempotency_key);
COMMENT ON TABLE billing_ledger_archive IS 'Retired claim fingerprints (replay protection). Sub2API S3 archive pattern.';

-- ----------------------------------------------------------------------------
-- Table: billing_events
-- ----------------------------------------------------------------------------
-- Audit-grade billing event row (in Tx2; survives Usage Record async failure).
-- Per F-OBS-001 H8: durable audit trail independent of Usage Record DLQ.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    claim_id                    bigint      NOT NULL REFERENCES billing_ledger_claims(id),
    event_type                  text        NOT NULL CHECK (event_type IN
                                    ('claim_committed', 'claim_aborted',
                                     'reconciliation_appended')),
    actual_cost                 numeric(20,8) NOT NULL DEFAULT 0,
    actual_cost_signed          numeric(20,8) NOT NULL DEFAULT 0,    -- for reconciliation deltas (positive or negative)
    end_class                   text,        -- F-GW-002 stream end taxonomy
    usage_source                text,        -- 'reported', 'normalized', 'inferred', 'partial', 'ambiguous'
    fingerprint                 text        NOT NULL,    -- request fingerprint
    occurred_at                 timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_billing_events_claim_time
    ON billing_events (claim_id, occurred_at);
CREATE INDEX idx_billing_events_tenant_type_time
    ON billing_events (tenant_id, event_type, occurred_at DESC);
COMMENT ON TABLE billing_events IS 'F-OBS-001 H8: audit-grade event row in Tx2; survives Usage Record async failure. Append-only.';

-- ----------------------------------------------------------------------------
-- Table: usage_records
-- ----------------------------------------------------------------------------
-- Rich analytics record (in Tx2 per F-OBS-001 H2).
-- usage_records 是不可变事实；对账通过追加事件表达。
-- Reconciliation appends new rows in usage_record_reconciliation_events.
-- Hot store; per-tenant retention policy.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS usage_records (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    claim_id                    bigint      NOT NULL REFERENCES billing_ledger_claims(id),
    api_key_id                  bigint      NOT NULL,
    user_id                     bigint      NOT NULL,
    provider_account_id         bigint      NOT NULL,
    acquisition_token           uuid        NOT NULL,
    attempt_seq                 integer     NOT NULL,
    -- Token counts
    tokens_input                integer     NOT NULL DEFAULT 0,
    tokens_output               integer     NOT NULL DEFAULT 0,
    cache_creation_tokens       integer     NOT NULL DEFAULT 0,
    cache_read_tokens           integer     NOT NULL DEFAULT 0,
    cache_creation_5m_tokens    integer     NOT NULL DEFAULT 0,
    cache_creation_1h_tokens    integer     NOT NULL DEFAULT 0,
    image_output_tokens         integer     NOT NULL DEFAULT 0,
    -- Money fields (numeric(20,8))
    actual_cost                 numeric(20,8) NOT NULL DEFAULT 0,
    input_cost                  numeric(20,8) NOT NULL DEFAULT 0,
    output_cost                 numeric(20,8) NOT NULL DEFAULT 0,
    cache_creation_cost         numeric(20,8) NOT NULL DEFAULT 0,
    cache_read_cost             numeric(20,8) NOT NULL DEFAULT 0,
    image_output_cost           numeric(20,8) NOT NULL DEFAULT 0,
    -- Stream end taxonomy
    end_class                   text        NOT NULL CHECK (end_class IN
                                    ('stream_end_graceful', 'stream_end_no_terminal_marker',
                                     'upstream_error_4xx', 'upstream_error_5xx',
                                     'upstream_rate_limit', 'upstream_auth_failure',
                                     'first_token_timeout', 'inter_event_timeout',
                                     'total_stream_timeout', 'client_disconnect',
                                     'event_size_exceeded', 'orchestrator_cancelled',
                                     'usage_ambiguous', 'unknown_termination',
                                     'non_streaming')),
    -- Usage source taxonomy
    usage_source                text        NOT NULL DEFAULT 'reported'
                                    CHECK (usage_source IN
                                    ('reported', 'normalized', 'inferred', 'partial', 'ambiguous')),
    confidence_score            numeric(5,4),    -- when usage_source='inferred'
    pending_reconciliation      boolean     NOT NULL DEFAULT false,
    drain_outcome               text,    -- 'max_seconds', 'max_bytes', 'max_estimated_cost', null
    -- Routing reason (F-POOL-001 structured payload, see spec §Audit / Usage / Log)
    routing_reason              jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Protocol loss field (F-PROTO-002 mandate)
    protocol_loss               jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- Stream timing
    requested_at                timestamptz NOT NULL,
    upstream_request_at         timestamptz,
    first_byte_at               timestamptz,
    first_event_at              timestamptz,
    last_event_at               timestamptz,
    settled_at                  timestamptz NOT NULL DEFAULT now(),
    -- Model
    requested_model             text        NOT NULL,
    upstream_model              text,
    -- Stream/non-stream
    stream                      boolean     NOT NULL DEFAULT false
);
CREATE INDEX idx_usage_records_tenant_settled
    ON usage_records (tenant_id, settled_at DESC);
CREATE INDEX idx_usage_records_user_settled
    ON usage_records (user_id, settled_at DESC);
CREATE INDEX idx_usage_records_pending_reconciliation
    ON usage_records (settled_at)
    WHERE pending_reconciliation = true;
CREATE INDEX idx_usage_records_claim
    ON usage_records (claim_id);
CREATE INDEX idx_usage_records_account_settled
    ON usage_records (provider_account_id, settled_at DESC);
COMMENT ON TABLE usage_records IS 'F-OBS-001: immutable usage record. Hot store. Reconciliation appends rows in usage_record_reconciliation_events; original is never mutated.';

-- ----------------------------------------------------------------------------
-- Table: usage_record_dlq
-- ----------------------------------------------------------------------------
-- Durable DLQ for Usage Record write failures (F-OBS-001 H10 / Helicone-inspired).
-- Persisted to PostgreSQL (NOT in-memory only).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS usage_record_dlq (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    claim_id                    bigint      NOT NULL REFERENCES billing_ledger_claims(id),
    payload                     jsonb       NOT NULL,    -- the failed Usage Record payload
    failure_reason              text        NOT NULL,
    failure_at                  timestamptz NOT NULL DEFAULT now(),
    -- Replay state
    replay_attempts             integer     NOT NULL DEFAULT 0,
    last_replay_at              timestamptz,
    replayed_at                 timestamptz,
    replay_failure_reason       text
);
CREATE INDEX idx_usage_dlq_tenant_unreplayed
    ON usage_record_dlq (tenant_id, failure_at)
    WHERE replayed_at IS NULL;
CREATE INDEX idx_usage_dlq_replayed
    ON usage_record_dlq (replayed_at DESC)
    WHERE replayed_at IS NOT NULL;
COMMENT ON TABLE usage_record_dlq IS 'F-OBS-001 H10: durable DLQ for Usage Record write failures. Operator-replayable; auto-replay cadence configurable.';

-- ----------------------------------------------------------------------------
-- Table: usage_record_reconciliation_events
-- ----------------------------------------------------------------------------
-- Append-only reconciliation events when authoritative usage arrives late.
-- Linked to original immutable Usage Record by original_usage_record_id.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS usage_record_reconciliation_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    original_usage_record_id    bigint      NOT NULL REFERENCES usage_records(id),
    -- Authoritative usage + delta
    authoritative_tokens_input  integer     NOT NULL DEFAULT 0,
    authoritative_tokens_output integer     NOT NULL DEFAULT 0,
    authoritative_cost          numeric(20,8) NOT NULL DEFAULT 0,
    cost_delta                  numeric(20,8) NOT NULL DEFAULT 0,    -- signed: authoritative - original
    -- Source of authority
    reconciliation_source       text        NOT NULL,    -- 'upstream_billing_api', 'manual_audit', etc.
    reconciled_at               timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_reconciliation_original
    ON usage_record_reconciliation_events (original_usage_record_id);
CREATE INDEX idx_reconciliation_tenant_time
    ON usage_record_reconciliation_events (tenant_id, reconciled_at DESC);
COMMENT ON TABLE usage_record_reconciliation_events IS 'F-OBS-001: append-only reconciliation events. Linked to immutable original Usage Record. Per-record reconciliation may produce paired billing_ledger_adjustments.';

-- ----------------------------------------------------------------------------
-- Table: billing_ledger_adjustments
-- ----------------------------------------------------------------------------
-- Append-only adjustment rows linked to original Billing Ledger claim.
-- Used for: late reconciliation, refund, dispute resolution.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_ledger_adjustments (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    original_claim_id           bigint      NOT NULL REFERENCES billing_ledger_claims(id),
    adjustment_type             text        NOT NULL CHECK (adjustment_type IN
                                    ('reconciliation', 'refund', 'dispute_resolution',
                                     'pricing_correction')),
    cost_delta                  numeric(20,8) NOT NULL,    -- signed
    reason                      text        NOT NULL,
    actor_id                    text,    -- operator id when manual
    -- Linked reconciliation event when adjustment_type='reconciliation'
    reconciliation_event_id     bigint      REFERENCES usage_record_reconciliation_events(id),
    created_at                  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_adjustments_claim
    ON billing_ledger_adjustments (original_claim_id);
CREATE INDEX idx_adjustments_tenant_type_time
    ON billing_ledger_adjustments (tenant_id, adjustment_type, created_at DESC);
COMMENT ON TABLE billing_ledger_adjustments IS 'F-OBS-001: append-only paired adjustments. Original claim never mutated; corrections via signed delta rows.';

-- ----------------------------------------------------------------------------
-- Table: billing_pricing_versions
-- ----------------------------------------------------------------------------
-- Versioned pricing context (F-BILL-001 framing element).
-- Each Usage Record carries billing_policy_version (claim row).
-- Reprice operations use stored historical version.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_pricing_versions (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    version                     text        NOT NULL,
    pricing_data                jsonb       NOT NULL,    -- per-model rate table
    effective_from              timestamptz NOT NULL,
    effective_to                timestamptz,    -- NULL = current
    created_at                  timestamptz NOT NULL DEFAULT now(),
    created_by_actor            text
);
CREATE UNIQUE INDEX uq_pricing_tenant_version
    ON billing_pricing_versions (tenant_id, version);
CREATE INDEX idx_pricing_effective
    ON billing_pricing_versions (tenant_id, effective_from DESC);
COMMENT ON TABLE billing_pricing_versions IS 'F-BILL-001 framing: versioned pricing context. Reprice operates on stored version, not live config.';

-- ----------------------------------------------------------------------------
-- Indexes summary
-- ----------------------------------------------------------------------------
-- Hot path:
--   uq_claims_idempotency                  - Tx1 claim insert ON CONFLICT
--   idx_claims_status_lease                - orphan sweep
--   idx_usage_records_tenant_settled       - operator dashboard query
--   idx_usage_records_pending_reconciliation - reconciliation worker scan
--   idx_usage_dlq_tenant_unreplayed        - DLQ replay worker
-- Audit path:
--   idx_billing_events_claim_time          - per-claim audit trail
--   idx_adjustments_claim                  - per-claim corrections trail
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- 固化日期：2026-04-28
-- 迁移顺序：0002（在账号池与路由之后）；后续只允许前向演进。
-- ----------------------------------------------------------------------------
