BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM passkey_credentials) THEN
        RAISE EXCEPTION 'refusing to roll back 0098: passkey credential data exists; production rollback requires an Owner-gated account-security data plan';
    END IF;
END $$;

DROP TABLE IF EXISTS webauthn_session;
DROP TABLE IF EXISTS passkey_credentials;

COMMIT;
