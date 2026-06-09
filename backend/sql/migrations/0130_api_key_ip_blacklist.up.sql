ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS ip_blacklist text DEFAULT NULL;
