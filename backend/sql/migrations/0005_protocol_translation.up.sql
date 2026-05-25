-- HUAKAI Phase 2 Schema Lock: protocol-translation
-- ============================================================================
-- Locks the schema surface required by docs/specs/protocol-translation.md
-- (F-PROTO-002).
-- DR-008 §1: schema fragments locked only after spec is Released.
--
-- Most F-PROTO-002 state is in-memory per-stream (UpstreamState +
-- ClientState). The persisted artifacts already exist via observability-billing.sql:
--   - usage_records.protocol_loss        jsonb (the F-PROTO-002 mandate)
--   - usage_records.upstream_model       text
--   - usage_records.requested_model      text
--
-- This fragment adds: capability matrix configuration (operator-tunable per
-- (client_protocol, upstream_protocol) cell) + protocol policy version registry.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- Table: protocol_capability_matrix
-- ----------------------------------------------------------------------------
-- Per-cell verdict: PRESERVED | LOSSY | UNSUPPORTED.
-- Operator UI shows this; tooling generates "what does my product support" docs.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS protocol_capability_matrix (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    -- Cell identity
    client_protocol             text        NOT NULL CHECK (client_protocol IN
                                    ('openai_chat', 'openai_responses', 'anthropic_messages')),
    upstream_protocol           text        NOT NULL CHECK (upstream_protocol IN
                                    ('anthropic', 'openai', 'gemini', 'bedrock', 'antigravity')),
    feature_name                text        NOT NULL CHECK (feature_name IN
                                    ('text_streaming', 'tool_use', 'reasoning_summary',
                                     'parallel_tool_calls', 'structured_output_schema',
                                     'image_input', 'audio_input', 'image_output',
                                     'max_tokens_finish_reason', 'max_completion_tokens',
                                     'stop_sequence_emit', 'cache_breakpoints',
                                     'signature_delta', 'system_prompt_array',
                                     'multi_role_messages')),
    -- Verdict per F-PROTO-002 §4.0 decision criteria
    verdict                     text        NOT NULL CHECK (verdict IN
                                    ('PRESERVED', 'LOSSY', 'UNSUPPORTED')),
    -- LOSSY note (HUAKAI-domain explanation, no upstream identifier names)
    loss_note                   text,
    -- Verification: which acceptance test asserts this cell
    verifying_test_id           text,    -- e.g. AT-PROTO-002-15
    -- Versioning
    matrix_policy_version       text        NOT NULL DEFAULT '1.0',
    last_verified_at            timestamptz NOT NULL DEFAULT now(),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_capability_matrix_cell
    ON protocol_capability_matrix (tenant_id, client_protocol, upstream_protocol, feature_name);
CREATE INDEX idx_capability_matrix_verdict
    ON protocol_capability_matrix (verdict, client_protocol, upstream_protocol)
    WHERE verdict != 'PRESERVED';
COMMENT ON TABLE protocol_capability_matrix IS 'F-PROTO-002 §4: per-(client × upstream × feature) capability verdict. Operator UI source. Tooling generates "supported features" docs.';

-- ----------------------------------------------------------------------------
-- Table: protocol_policy_versions
-- ----------------------------------------------------------------------------
-- Versioned protocol policy (capability matrix + signature_delta carry-forward
-- + safe-equivalent settings). Each Usage Record references the version active
-- at translation time.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS protocol_policy_versions (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    version                     text        NOT NULL,
    -- Per-Route policy snapshot
    policy_data                 jsonb       NOT NULL,
        -- jsonb shape: { signature_delta_carry_forward: bool, safe_equivalent_default: enum, ... }
    effective_from              timestamptz NOT NULL,
    effective_to                timestamptz,    -- NULL = current
    created_at                  timestamptz NOT NULL DEFAULT now(),
    created_by_actor            text
);
CREATE UNIQUE INDEX uq_protocol_policy_tenant_version
    ON protocol_policy_versions (tenant_id, version);
CREATE INDEX idx_protocol_policy_effective
    ON protocol_policy_versions (tenant_id, effective_from DESC);
COMMENT ON TABLE protocol_policy_versions IS 'F-PROTO-002: versioned policy. Usage Records reference the version active at translation time for replay correctness.';

-- ----------------------------------------------------------------------------
-- Indexes summary
-- ----------------------------------------------------------------------------
-- Hot path:
--   uq_capability_matrix_cell           - Phase A capability lookup per request
--   idx_capability_matrix_verdict       - operator UI: "show me LOSSY/UNSUPPORTED cells"
--   uq_protocol_policy_tenant_version   - Tx2 policy version lookup
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- NOT in this fragment (lives elsewhere)
-- ----------------------------------------------------------------------------
-- usage_records.protocol_loss          - in observability-billing.sql
-- routes.signature_delta_carry_forward - per-Route override; can be added to
--                                        routes table if operator override needed
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- Locked: 2026-04-28
-- Spec source: docs/specs/protocol-translation.md @ Status=Released
-- Migration order: 0005 (after rate-limiting). Forward-only.
-- ----------------------------------------------------------------------------
