-- 0236:平台级模型绝对价覆盖。官方基准价仍留在 billing_pricing_versions;
-- 本表只存部署者写入的美元单价差量。删除覆盖即回官方价。
-- 单位 = micro-USD/token(= USD / 1M tokens)。变更日志是永久资金事实,
-- 不参与 30 天普通日志清理。

BEGIN;

CREATE TABLE IF NOT EXISTS model_rate_overrides (
    id                          bigserial PRIMARY KEY,
    vendor                      text NOT NULL,
    model                       text NOT NULL,
    input_micro_usd             numeric(20, 8),
    output_micro_usd            numeric(20, 8),
    cache_read_micro_usd        numeric(20, 8),
    cache_creation_micro_usd    numeric(20, 8),
    cache_creation_5m_micro_usd numeric(20, 8),
    cache_creation_1h_micro_usd numeric(20, 8),
    created_by                  text NOT NULL,
    updated_by                  text NOT NULL,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_model_rate_overrides_vendor_model UNIQUE (vendor, model),
    CONSTRAINT model_rate_overrides_vendor_shape
        CHECK (vendor ~ '^[a-z][a-z0-9_-]{0,31}$'),
    CONSTRAINT model_rate_overrides_model_shape
        CHECK (char_length(model) BETWEEN 1 AND 128),
    CONSTRAINT model_rate_overrides_has_bucket
        CHECK (
            input_micro_usd IS NOT NULL
            OR output_micro_usd IS NOT NULL
            OR cache_read_micro_usd IS NOT NULL
            OR cache_creation_micro_usd IS NOT NULL
            OR cache_creation_5m_micro_usd IS NOT NULL
            OR cache_creation_1h_micro_usd IS NOT NULL
        ),
    CONSTRAINT model_rate_overrides_nonneg
        CHECK (
            (input_micro_usd IS NULL OR input_micro_usd >= 0)
            AND (output_micro_usd IS NULL OR output_micro_usd >= 0)
            AND (cache_read_micro_usd IS NULL OR cache_read_micro_usd >= 0)
            AND (cache_creation_micro_usd IS NULL OR cache_creation_micro_usd >= 0)
            AND (cache_creation_5m_micro_usd IS NULL OR cache_creation_5m_micro_usd >= 0)
            AND (cache_creation_1h_micro_usd IS NULL OR cache_creation_1h_micro_usd >= 0)
        )
);

CREATE INDEX IF NOT EXISTS idx_model_rate_overrides_vendor
    ON model_rate_overrides (vendor);

COMMENT ON TABLE model_rate_overrides IS
    'Platform-wide absolute USD rate overrides per vendor+model. Missing row means official catalog price.';

CREATE TABLE IF NOT EXISTS model_rate_override_audit_log (
    id          bigserial PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    actor_id    text NOT NULL,
    actor_role  text NOT NULL,
    vendor      text NOT NULL,
    model       text NOT NULL,
    action      text NOT NULL CHECK (action IN ('upsert', 'delete')),
    old_payload jsonb,
    new_payload jsonb,
    prev_hash   bytea,
    entry_hash  bytea NOT NULL CHECK (octet_length(entry_hash) = 32),
    signature   bytea NOT NULL CHECK (octet_length(signature) = 64),
    key_id      text NOT NULL,
    CONSTRAINT model_rate_override_audit_prev_hash_len
        CHECK (prev_hash IS NULL OR octet_length(prev_hash) = 32),
    CONSTRAINT model_rate_override_audit_payload_shape
        CHECK (
            (action = 'upsert' AND new_payload IS NOT NULL)
            OR
            (action = 'delete' AND old_payload IS NOT NULL AND new_payload IS NULL)
        )
);

CREATE INDEX IF NOT EXISTS idx_model_rate_override_audit_scope_time
    ON model_rate_override_audit_log (vendor, model, occurred_at);

COMMENT ON TABLE model_rate_override_audit_log IS
    'Append-only signed hash-chain money log for model_rate_overrides. Permanent; not subject to 30-day log cleanup.';

COMMIT;
