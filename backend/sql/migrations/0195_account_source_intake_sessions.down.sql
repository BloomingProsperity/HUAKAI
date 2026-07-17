BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM account_source_intake_sessions
        WHERE status = 'ready' AND expires_at > now()
    ) THEN
        RAISE EXCEPTION 'refusing rollback: account source intake sessions are still active';
    END IF;
END $$;

DROP TABLE IF EXISTS account_source_intake_sessions;

COMMIT;
