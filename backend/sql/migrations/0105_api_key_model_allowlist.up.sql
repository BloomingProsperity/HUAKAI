BEGIN;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS allowed_models text;

COMMENT ON COLUMN api_keys.allowed_models IS
    'Comma-separated canonical model ids allowed for this inbound API key. NULL/blank means unrestricted.';

COMMIT;
