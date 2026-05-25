BEGIN;

DROP INDEX IF EXISTS idx_audit_signer_pubkeys_effective_from;
DROP INDEX IF EXISTS idx_audit_signer_pubkeys_active_algorithm;
DROP TABLE IF EXISTS audit_signer_pubkeys;

COMMIT;
