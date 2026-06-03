BEGIN;

CREATE TABLE IF NOT EXISTS moderation_keywords (
    id          bigserial   PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants(id),
    keyword     text        NOT NULL CHECK (length(btrim(keyword)) > 0),
    reason_code text        NOT NULL DEFAULT 'keyword_match',
    enabled     boolean     NOT NULL DEFAULT true,
    created_by  text,
    updated_by  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_moderation_keywords_tenant_keyword
    ON moderation_keywords (tenant_id, lower(keyword))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_moderation_keywords_enabled
    ON moderation_keywords (tenant_id, enabled)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS moderation_hashes (
    id          bigserial   PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants(id),
    hash_hex    text        NOT NULL CHECK (hash_hex ~ '^[0-9a-f]{64}$'),
    reason_code text        NOT NULL DEFAULT 'hash_match',
    enabled     boolean     NOT NULL DEFAULT true,
    created_by  text,
    updated_by  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_moderation_hashes_tenant_hash
    ON moderation_hashes (tenant_id, hash_hex)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_moderation_hashes_enabled
    ON moderation_hashes (tenant_id, enabled)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS moderation_config (
    tenant_id           bigint      PRIMARY KEY REFERENCES tenants(id),
    enabled             boolean     NOT NULL DEFAULT false,
    fail_closed         boolean     NOT NULL DEFAULT true,
    sample_rate_pct     integer     NOT NULL DEFAULT 100
                                      CHECK (sample_rate_pct >= 0 AND sample_rate_pct <= 100),
    ban_threshold       integer     NOT NULL DEFAULT 3 CHECK (ban_threshold >= 0),
    ban_window_seconds  integer     NOT NULL DEFAULT 3600 CHECK (ban_window_seconds > 0),
    violation_fee_usd   numeric(20, 8) NOT NULL DEFAULT 0 CHECK (violation_fee_usd >= 0),
    updated_by          text,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS moderation_log (
    id                 bigserial   PRIMARY KEY,
    tenant_id          bigint      NOT NULL REFERENCES tenants(id),
    api_key_id         bigint      NOT NULL,
    user_id            bigint      NOT NULL,
    request_id         text,
    payload_hash       text        NOT NULL CHECK (length(btrim(payload_hash)) > 0),
    decision           text        NOT NULL CHECK (decision IN
        ('pass', 'block_keyword', 'block_hash', 'block_backend', 'fee_charged')),
    reason_code        text        NOT NULL,
    matched_keyword_id bigint,
    matched_hash_id    bigint,
    violation_fee_usd  numeric(20, 8) NOT NULL DEFAULT 0 CHECK (violation_fee_usd >= 0),
    billing_event_id   bigint,
    occurred_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_moderation_log_tenant_time
    ON moderation_log (tenant_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_moderation_log_api_key_blocks
    ON moderation_log (tenant_id, api_key_id, occurred_at DESC)
    WHERE decision IN ('block_keyword', 'block_hash');

CREATE INDEX IF NOT EXISTS idx_moderation_log_payload_hash
    ON moderation_log (tenant_id, payload_hash);

COMMENT ON TABLE moderation_log IS
    'Content moderation audit. Stores payload hashes and match metadata only; raw request bodies and credentials are forbidden.';

COMMIT;
