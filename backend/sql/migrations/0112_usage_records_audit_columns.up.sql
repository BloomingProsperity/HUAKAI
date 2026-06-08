ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS image_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS image_size text NULL,
    ADD COLUMN IF NOT EXISTS image_size_breakdown jsonb NULL DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS ip_address text NULL,
    ADD COLUMN IF NOT EXISTS user_agent text NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'usage_records_user_agent_len_check'
          AND conrelid = 'usage_records'::regclass
    ) THEN
        ALTER TABLE usage_records
            ADD CONSTRAINT usage_records_user_agent_len_check
            CHECK (user_agent IS NULL OR length(user_agent) <= 512) NOT VALID;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_usage_records_ip_address
    ON usage_records (ip_address)
    WHERE ip_address IS NOT NULL;
