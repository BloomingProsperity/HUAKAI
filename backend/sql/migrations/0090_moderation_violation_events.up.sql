BEGIN;

CREATE TABLE IF NOT EXISTS moderation_violation_events (
    id                 bigserial   PRIMARY KEY,
    tenant_id          bigint      NOT NULL REFERENCES tenants(id),
    api_key_id         bigint      NOT NULL,
    user_id            bigint      NOT NULL,
    request_id         text,
    payload_hash       text        NOT NULL CHECK (length(btrim(payload_hash)) > 0),
    decision           text        NOT NULL CHECK (decision IN ('block_keyword', 'block_hash')),
    reason_code        text        NOT NULL,
    matched_keyword_id bigint,
    matched_hash_id    bigint,
    occurred_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_moderation_violation_events_api_key_window
    ON moderation_violation_events (tenant_id, api_key_id, occurred_at DESC);

COMMENT ON TABLE moderation_violation_events IS
    'Non-sampled content moderation block events used only for auto-ban windows; stores metadata and payload hashes, never raw bodies or credentials.';

COMMIT;
