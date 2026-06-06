-- HUAKAI tenant-scoped alert rules, fired events, and silences.

BEGIN;

CREATE TABLE IF NOT EXISTS alert_rules (
    id             BIGSERIAL   PRIMARY KEY,
    tenant_id      BIGINT      NOT NULL REFERENCES tenants(id),
    name           TEXT        NOT NULL,
    metric         TEXT        NOT NULL,
    comparator     TEXT        NOT NULL,
    threshold      NUMERIC     NOT NULL,
    severity       TEXT        NOT NULL,
    window_seconds INTEGER     NOT NULL CHECK (window_seconds > 0),
    enabled        BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name),
    UNIQUE (tenant_id, id),
    CHECK (
        comparator IN ('gt', 'gte', 'lt', 'lte')
        AND severity IN ('info', 'warning', 'critical')
    )
);

CREATE TABLE IF NOT EXISTS alert_events (
    id             BIGSERIAL   PRIMARY KEY,
    tenant_id      BIGINT      NOT NULL REFERENCES tenants(id),
    rule_id        BIGINT      NOT NULL,
    state          TEXT        NOT NULL CHECK (state IN ('firing', 'resolved')),
    observed_value NUMERIC     NOT NULL,
    fired_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ NULL,
    FOREIGN KEY (tenant_id, rule_id) REFERENCES alert_rules (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_alert_events_tenant_rule_state
    ON alert_events (tenant_id, rule_id, state);

CREATE TABLE IF NOT EXISTS alert_silences (
    id         BIGSERIAL   PRIMARY KEY,
    tenant_id  BIGINT      NOT NULL REFERENCES tenants(id),
    rule_id    BIGINT      NULL,
    reason     TEXT        NOT NULL,
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    FOREIGN KEY (tenant_id, rule_id) REFERENCES alert_rules (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_alert_silences_tenant_ends_at
    ON alert_silences (tenant_id, ends_at);

COMMIT;
