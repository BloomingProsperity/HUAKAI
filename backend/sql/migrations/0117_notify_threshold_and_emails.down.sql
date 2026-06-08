ALTER TABLE user_notification_settings
    DROP COLUMN IF EXISTS threshold_type,
    DROP COLUMN IF EXISTS extra_emails;
