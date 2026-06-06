-- HUAKAI announcements board.
-- Adds tenant-scoped admin CRUD storage plus public/user active announcement reads.

BEGIN;

CREATE TABLE IF NOT EXISTS announcements (
    id               BIGSERIAL   PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    title            TEXT        NOT NULL,
    body             TEXT        NOT NULL,
    severity         TEXT        NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'warning', 'critical')),
    active           BOOLEAN     NOT NULL DEFAULT true,
    published_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NULL,
    created_by_admin BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_announcements_tenant_active_published
    ON announcements (tenant_id, active, published_at DESC);

COMMIT;
