BEGIN;

CREATE TABLE IF NOT EXISTS media_tasks (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    user_id          BIGINT      NOT NULL,
    task_type        TEXT        NOT NULL,
    status           TEXT        NOT NULL
        CHECK (status IN ('queued', 'in_progress', 'succeeded', 'failed', 'expired')),
    provider         TEXT        NOT NULL,
    provider_task_id TEXT,
    request_id       TEXT        NOT NULL,
    input_params     JSONB       NOT NULL,
    result           JSONB,
    estimated_cents  BIGINT      NOT NULL CHECK (estimated_cents >= 0),
    actual_cents     BIGINT      CHECK (actual_cents IS NULL OR actual_cents >= 0),
    hold_ref         TEXT,
    error_class      TEXT,
    progress         SMALLINT    NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    lease_owner      TEXT,
    lease_expires_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    UNIQUE (tenant_id, request_id),
    CONSTRAINT fk_media_tasks_user
        FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_media_tasks_tenant_user_created
    ON media_tasks (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_media_tasks_runnable_status
    ON media_tasks (status)
    WHERE status IN ('queued', 'in_progress');

COMMIT;
