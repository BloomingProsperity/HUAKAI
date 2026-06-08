DROP INDEX IF EXISTS idx_usage_records_ip_address;

ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_user_agent_len_check,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS ip_address,
    DROP COLUMN IF EXISTS image_size_breakdown,
    DROP COLUMN IF EXISTS image_size,
    DROP COLUMN IF EXISTS image_count;
