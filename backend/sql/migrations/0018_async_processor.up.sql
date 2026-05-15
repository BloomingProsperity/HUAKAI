-- 0018_async_processor.up.sql
--
-- F-OBS-004: async processor chain primary event ledger.
-- v1 keeps the table deliberately small: payload stores only redacted
-- references and hashes, not raw request/response bodies.

BEGIN;

CREATE TABLE IF NOT EXISTS async_processor_events (
    id bigserial PRIMARY KEY,
    event_kind text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    handler_state text NOT NULL DEFAULT 'inflight',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT async_processor_events_handler_state_check
        CHECK (handler_state IN ('done', 'inflight', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_async_processor_events_state_created
    ON async_processor_events (handler_state, created_at, id);

CREATE INDEX IF NOT EXISTS idx_async_processor_events_kind_created
    ON async_processor_events (event_kind, created_at DESC, id DESC);

COMMENT ON TABLE async_processor_events IS
    'F-OBS-004 async processor event ledger. Payload must contain redacted refs and hashes only in v1.';
COMMENT ON COLUMN async_processor_events.handler_state IS
    'Aggregate chain state: done, inflight, or failed.';

COMMIT;
