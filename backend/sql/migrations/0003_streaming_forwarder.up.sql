-- HUAKAI 流式转发基础结构
-- ============================================================================
-- 提供 F-GW-002 所需表面。
--
-- Most F-GW-002 state lives in tables already locked by:
--   - pool-routing.sql (provider_accounts, pool_slot_acquisitions, routes)
--   - observability-billing.sql (billing_ledger_claims, usage_records,
--     usage_record_dlq, billing_events)
--
-- F-GW-002 adds: per-Route streaming policy columns (timeout axes, drain
-- budgets, scanner buffer cap, mid-stream-failover opt-in default,
-- failover status code overrides).
-- ============================================================================

-- ----------------------------------------------------------------------------
-- ALTER TABLE: routes — F-GW-002 streaming policy columns
-- ----------------------------------------------------------------------------
-- These columns extend the routes table from pool-routing.sql with streaming
-- policy. Default values match spec defaults; operator-tunable per Route.
-- ----------------------------------------------------------------------------
ALTER TABLE routes
    -- Eight-axis timeout policy (F-GW-002 §5.2)
    ADD COLUMN IF NOT EXISTS connect_timeout_ms          integer NOT NULL DEFAULT 5000,
    ADD COLUMN IF NOT EXISTS tls_handshake_timeout_ms    integer NOT NULL DEFAULT 5000,
    ADD COLUMN IF NOT EXISTS request_write_timeout_ms    integer NOT NULL DEFAULT 30000,
    ADD COLUMN IF NOT EXISTS response_header_timeout_ms  integer NOT NULL DEFAULT 30000,
    ADD COLUMN IF NOT EXISTS first_token_timeout_ms      integer NOT NULL DEFAULT 60000,
    ADD COLUMN IF NOT EXISTS inter_event_timeout_ms      integer NOT NULL DEFAULT 30000,
    ADD COLUMN IF NOT EXISTS total_stream_timeout_ms     integer NOT NULL DEFAULT 600000,
    ADD COLUMN IF NOT EXISTS downstream_write_timeout_ms integer NOT NULL DEFAULT 10000,
    -- Scanner buffer cap (F-GW-002 §5.1)
    ADD COLUMN IF NOT EXISTS scanner_buffer_max_bytes    integer NOT NULL DEFAULT 1048576    -- 1 MiB
                                CHECK (scanner_buffer_max_bytes <= 67108864),  -- max 64 MiB cap
    -- Bounded drain budgets (F-GW-002 §Phase C-bis)
    ADD COLUMN IF NOT EXISTS drain_max_seconds           integer NOT NULL DEFAULT 30,
    ADD COLUMN IF NOT EXISTS drain_max_bytes             integer NOT NULL DEFAULT 1048576,   -- 1 MiB
    ADD COLUMN IF NOT EXISTS drain_max_estimated_cost_usd numeric(20,8) NOT NULL DEFAULT 0.10,
    -- Mid-stream failover (F-GW-002 H6)
    ADD COLUMN IF NOT EXISTS mid_stream_failover_default boolean NOT NULL DEFAULT false,
    -- Tokenizer fallback for missing terminal usage (F-GW-002 H5)
    ADD COLUMN IF NOT EXISTS tokenizer_fallback_enabled  boolean NOT NULL DEFAULT true;

COMMENT ON COLUMN routes.connect_timeout_ms IS 'F-GW-002 timeout axis 1 (TCP connect).';
COMMENT ON COLUMN routes.tls_handshake_timeout_ms IS 'F-GW-002 timeout axis 2 (TLS).';
COMMENT ON COLUMN routes.request_write_timeout_ms IS 'F-GW-002 timeout axis 3 (request body write).';
COMMENT ON COLUMN routes.response_header_timeout_ms IS 'F-GW-002 timeout axis 4 (upstream response headers).';
COMMENT ON COLUMN routes.first_token_timeout_ms IS 'F-GW-002 timeout axis 5 (first event after headers).';
COMMENT ON COLUMN routes.inter_event_timeout_ms IS 'F-GW-002 timeout axis 6 (gap between events).';
COMMENT ON COLUMN routes.total_stream_timeout_ms IS 'F-GW-002 timeout axis 7 (whole stream cap).';
COMMENT ON COLUMN routes.downstream_write_timeout_ms IS 'F-GW-002 timeout axis 8 (flush to client).';
COMMENT ON COLUMN routes.scanner_buffer_max_bytes IS 'F-GW-002 §5.1: bounded scanner buffer; 1 MiB default vs upstream 500 MiB; max 64 MiB cap.';
COMMENT ON COLUMN routes.drain_max_seconds IS 'F-GW-002 §Phase C-bis: drain time budget on client disconnect.';
COMMENT ON COLUMN routes.drain_max_bytes IS 'F-GW-002 §Phase C-bis: drain byte budget on client disconnect.';
COMMENT ON COLUMN routes.drain_max_estimated_cost_usd IS 'F-GW-002 §Phase C-bis: drain cost budget on client disconnect.';
COMMENT ON COLUMN routes.mid_stream_failover_default IS 'F-GW-002 H6: default off; client may opt in via Idempotent-Stream-Replay header.';

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- 固化日期：2026-04-28
-- 迁移顺序：0003（在观测与计费之后）；后续只允许前向演进。
-- ----------------------------------------------------------------------------
