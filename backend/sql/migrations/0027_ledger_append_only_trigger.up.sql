BEGIN;

CREATE OR REPLACE FUNCTION enforce_ledger_append_only() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'audit_ledger_entries is append-only: %', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_append_only_update ON audit_ledger_entries;
CREATE TRIGGER ledger_append_only_update BEFORE UPDATE ON audit_ledger_entries
  FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();

DROP TRIGGER IF EXISTS ledger_append_only_delete ON audit_ledger_entries;
CREATE TRIGGER ledger_append_only_delete BEFORE DELETE ON audit_ledger_entries
  FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();

COMMIT;
