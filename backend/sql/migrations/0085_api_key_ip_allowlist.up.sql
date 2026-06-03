BEGIN;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS ip_allowlist text;

COMMENT ON COLUMN api_keys.ip_allowlist IS
    'Comma-separated canonical CIDR list for inbound API key client IP restrictions. NULL/blank means unrestricted.';

COMMIT;
