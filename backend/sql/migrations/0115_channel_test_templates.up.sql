-- HUAKAI channel health probe test templates.
-- Stores operator-authored templates only; no paid probe execution is wired here.

BEGIN;

CREATE TABLE IF NOT EXISTS channel_test_templates (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    name            text        NOT NULL,
    method          text        NOT NULL,
    path            text        NOT NULL,
    body_template   text        NOT NULL DEFAULT '',
    headers         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_channel_test_templates_tenant_name
    ON channel_test_templates (tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_channel_test_templates_tenant_created
    ON channel_test_templates (tenant_id, created_at DESC, id DESC);

COMMENT ON TABLE channel_test_templates IS
    'OBS-175: admin-authored channel probe request templates. Templates are persisted only; execution is a separate explicit action.';
COMMENT ON COLUMN channel_test_templates.body_template IS
    'Operator-authored request template. Must not be populated from upstream response bodies.';
COMMENT ON COLUMN channel_test_templates.headers IS
    'Template headers JSON object. Admin API validation rejects credential-bearing headers.';

COMMIT;
