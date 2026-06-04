BEGIN;

CREATE TABLE IF NOT EXISTS user_notification_settings (
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    notify_type TEXT NOT NULL DEFAULT 'none',
    webhook_url TEXT NOT NULL DEFAULT '',
    webhook_secret TEXT NOT NULL DEFAULT '',
    notification_email TEXT NOT NULL DEFAULT '',
    bark_url TEXT NOT NULL DEFAULT '',
    gotify_url TEXT NOT NULL DEFAULT '',
    gotify_token TEXT NOT NULL DEFAULT '',
    gotify_priority INT NOT NULL DEFAULT 5,
    balance_threshold NUMERIC(20,8) NOT NULL DEFAULT 5.00000000,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL DEFAULT 'system',
    PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT fk_user_notification_settings_user
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT chk_user_notification_settings_type
        CHECK (notify_type IN ('none', 'email', 'webhook', 'bark', 'gotify')),
    CONSTRAINT chk_user_notification_settings_threshold
        CHECK (balance_threshold >= 0),
    CONSTRAINT chk_user_notification_settings_gotify_priority
        CHECK (gotify_priority BETWEEN 0 AND 10),
    CONSTRAINT chk_user_notification_settings_target
        CHECK (
            notify_type = 'none'
            OR (notify_type = 'email' AND notification_email <> '')
            OR (notify_type = 'webhook' AND webhook_url <> '' AND webhook_secret <> '')
            OR (notify_type = 'bark' AND bark_url <> '')
            OR (notify_type = 'gotify' AND gotify_url <> '' AND gotify_token <> '')
        )
);

CREATE INDEX IF NOT EXISTS idx_user_notification_settings_active
    ON user_notification_settings (tenant_id, notify_type)
    WHERE notify_type <> 'none';

COMMENT ON TABLE user_notification_settings IS
    'Per-user notification delivery settings. Defaults to notify_type=none so users receive nothing until configured.';

COMMIT;
