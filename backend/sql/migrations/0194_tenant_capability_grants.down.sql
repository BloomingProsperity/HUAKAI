BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM tenant_capability_grants) OR
       EXISTS (SELECT 1 FROM tenant_capability_grant_events) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55006',
            MESSAGE = 'cannot rollback migration 0194: tenant capability grant history still exists',
            HINT = 'export and explicitly remove tenant capability grants and events before retrying the rollback';
    END IF;
END
$$;

DROP TABLE tenant_capability_grant_events;
DROP TABLE tenant_capability_grants;

COMMIT;
