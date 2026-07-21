BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM balance_ledger_transactions) THEN
        RAISE EXCEPTION 'refusing down 0209: permanent balance ledger transactions exist';
    END IF;
    IF EXISTS (SELECT 1 FROM tenant_wallets WHERE balance <> 0) THEN
        RAISE EXCEPTION 'refusing down 0209: non-zero tenant wallets exist';
    END IF;
END $$;

DROP TRIGGER IF EXISTS balance_ledger_entries_append_only_delete ON balance_ledger_entries;
DROP TRIGGER IF EXISTS balance_ledger_entries_append_only_update ON balance_ledger_entries;
DROP TRIGGER IF EXISTS balance_ledger_transactions_append_only_delete ON balance_ledger_transactions;
DROP TRIGGER IF EXISTS balance_ledger_transactions_append_only_update ON balance_ledger_transactions;
DROP TRIGGER IF EXISTS balance_ledger_transaction_shape ON balance_ledger_entries;
DROP TRIGGER IF EXISTS balance_ledger_transaction_complete ON balance_ledger_transactions;
DROP FUNCTION IF EXISTS enforce_balance_ledger_transaction_shape();
DROP TABLE balance_ledger_entries;
DROP TABLE balance_ledger_transactions;
DROP TABLE tenant_wallets;

COMMIT;
