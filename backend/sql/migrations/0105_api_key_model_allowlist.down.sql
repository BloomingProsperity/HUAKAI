BEGIN;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS allowed_models;

COMMIT;
