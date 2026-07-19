BEGIN;

CREATE TABLE provider_account_quota_facts (
    tenant_id                  bigint        NOT NULL,
    provider_account_id       bigint        NOT NULL,
    vendor                     text          NOT NULL CHECK (btrim(vendor) <> ''),
    metric_key                 text          NOT NULL CHECK (btrim(metric_key) <> ''),
    model_key                  text          NOT NULL DEFAULT '',
    state                      text          NOT NULL CHECK (state IN ('available', 'exhausted', 'unknown', 'error')),
    used_value                 numeric(20,8),
    limit_value                numeric(20,8),
    remaining_value            numeric(20,8),
    unit                        text,
    utilization_percent        numeric(7,4) CHECK (utilization_percent BETWEEN 0 AND 100),
    remaining_percent          numeric(7,4) CHECK (remaining_percent BETWEEN 0 AND 100),
    resets_at                  timestamptz,
    observed_at                timestamptz   NOT NULL,
    valid_until                timestamptz,
    source                     text          NOT NULL CHECK (source IN ('upstream_usage', 'upstream_billing', 'upstream_model_catalog', 'response_headers', 'capability_contract')),
    error_class                text,
    created_at                 timestamptz   NOT NULL DEFAULT now(),
    updated_at                 timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider_account_id, metric_key, model_key),
    CONSTRAINT provider_account_quota_facts_account_fkey
        FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT provider_account_quota_facts_shape_check CHECK (
        (state = 'error' AND error_class IS NOT NULL)
        OR (state <> 'error' AND error_class IS NULL)
    ),
    CONSTRAINT provider_account_quota_facts_unknown_shape_check CHECK (
        state <> 'unknown'
        OR (
            used_value IS NULL
            AND limit_value IS NULL
            AND remaining_value IS NULL
            AND utilization_percent IS NULL
            AND remaining_percent IS NULL
        )
    )
);

CREATE INDEX idx_provider_account_quota_facts_account
    ON provider_account_quota_facts (tenant_id, provider_account_id, observed_at DESC);
CREATE INDEX idx_provider_account_quota_facts_model
    ON provider_account_quota_facts (tenant_id, model_key, state, observed_at DESC)
    WHERE model_key <> '';

CREATE TABLE provider_account_routing_signals (
    tenant_id                  bigint        NOT NULL,
    provider_account_id       bigint        NOT NULL,
    success_ewma               double precision NOT NULL DEFAULT 0 CHECK (success_ewma BETWEEN 0 AND 1),
    error_ewma                 double precision NOT NULL DEFAULT 0 CHECK (error_ewma BETWEEN 0 AND 1),
    response_latency_ms_ewma   double precision CHECK (response_latency_ms_ewma >= 0),
    sample_count               bigint        NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    last_outcome               text          NOT NULL CHECK (last_outcome IN ('success', 'error')),
    last_success_at            timestamptz,
    last_error_at              timestamptz,
    observed_at                timestamptz   NOT NULL,
    updated_at                 timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider_account_id),
    CONSTRAINT provider_account_routing_signals_account_fkey
        FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_provider_account_routing_signals_fresh
    ON provider_account_routing_signals (tenant_id, observed_at DESC);

COMMENT ON TABLE provider_account_quota_facts IS '上游账号额度的规范化事实投影；未知值保持未知，不推导为满额或零额度。';
COMMENT ON TABLE provider_account_routing_signals IS '跨实例共享的账号请求结果与响应头耗时 EWMA，用于可解释调度。';

COMMIT;
