BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM media_tasks) THEN
        RAISE EXCEPTION 'refusing to roll back 0099: media task data exists; production rollback requires an Owner-gated data plan';
    END IF;
END $$;

DROP TABLE IF EXISTS media_tasks;

COMMIT;
