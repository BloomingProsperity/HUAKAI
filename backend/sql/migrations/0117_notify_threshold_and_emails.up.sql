ALTER TABLE user_notification_settings
    ADD COLUMN IF NOT EXISTS threshold_type text NOT NULL DEFAULT 'fixed'
        CHECK (threshold_type IN ('fixed','percentage')),
    ADD COLUMN IF NOT EXISTS extra_emails text NOT NULL DEFAULT '[]';
