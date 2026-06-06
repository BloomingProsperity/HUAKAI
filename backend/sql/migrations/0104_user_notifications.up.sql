-- HUAKAI per-user notification inbox.
-- Adds admin broadcast fan-out storage with per-recipient read state.

BEGIN;

CREATE TABLE IF NOT EXISTS user_notifications (
    id               BIGSERIAL   PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    user_id          BIGINT      NOT NULL,
    title            TEXT        NOT NULL,
    body             TEXT        NOT NULL,
    severity         TEXT        NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'warning', 'critical')),
    read_at          TIMESTAMPTZ NULL,
    created_by_admin BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_user_notifications_tenant_user_read
    ON user_notifications (tenant_id, user_id, read_at);

COMMIT;
