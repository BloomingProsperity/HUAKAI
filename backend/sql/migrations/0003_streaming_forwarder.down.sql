-- Down migration for 0003_streaming_forwarder.

BEGIN;

ALTER TABLE routes
    DROP COLUMN IF EXISTS tokenizer_fallback_enabled,
    DROP COLUMN IF EXISTS mid_stream_failover_default,
    DROP COLUMN IF EXISTS drain_max_estimated_cost_usd,
    DROP COLUMN IF EXISTS drain_max_bytes,
    DROP COLUMN IF EXISTS drain_max_seconds,
    DROP COLUMN IF EXISTS scanner_buffer_max_bytes,
    DROP COLUMN IF EXISTS downstream_write_timeout_ms,
    DROP COLUMN IF EXISTS total_stream_timeout_ms,
    DROP COLUMN IF EXISTS inter_event_timeout_ms,
    DROP COLUMN IF EXISTS first_token_timeout_ms,
    DROP COLUMN IF EXISTS response_header_timeout_ms,
    DROP COLUMN IF EXISTS request_write_timeout_ms,
    DROP COLUMN IF EXISTS tls_handshake_timeout_ms,
    DROP COLUMN IF EXISTS connect_timeout_ms;

COMMIT;
