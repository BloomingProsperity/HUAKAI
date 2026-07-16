BEGIN;

DROP INDEX IF EXISTS idx_account_credentials_material_fingerprint;
DROP INDEX IF EXISTS idx_account_credentials_external_subject;

ALTER TABLE account_credentials
    DROP COLUMN IF EXISTS credential_material_fingerprint,
    DROP COLUMN IF EXISTS external_identity_source,
    DROP COLUMN IF EXISTS external_subject_id;

COMMIT;
