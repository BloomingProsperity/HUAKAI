BEGIN;

CREATE TABLE IF NOT EXISTS daily_checkin (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL,
    user_id          BIGINT      NOT NULL,
    checkin_date     DATE        NOT NULL,
    reward_cents     BIGINT      NOT NULL CHECK (reward_cents >= 0),
    currency_code    TEXT        NOT NULL DEFAULT 'USD',
    billing_event_id BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_daily_checkin_tenant_user_date UNIQUE (tenant_id, user_id, checkin_date)
);

CREATE INDEX IF NOT EXISTS idx_daily_checkin_tenant_user_date
    ON daily_checkin (tenant_id, user_id, checkin_date);

COMMIT;
