BEGIN;
ALTER TABLE provider_accounts DROP COLUMN IF EXISTS tls_fingerprint_rotate;
COMMIT;
