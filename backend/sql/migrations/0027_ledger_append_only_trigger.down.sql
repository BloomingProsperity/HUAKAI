BEGIN;

DROP TRIGGER IF EXISTS ledger_append_only_update ON audit_ledger_entries;
DROP TRIGGER IF EXISTS ledger_append_only_delete ON audit_ledger_entries;
DROP FUNCTION IF EXISTS enforce_ledger_append_only();

COMMIT;
