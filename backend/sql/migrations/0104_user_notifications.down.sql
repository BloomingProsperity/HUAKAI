-- Roll back HUAKAI per-user notification inbox storage.

BEGIN;

DROP TABLE IF EXISTS user_notifications;

COMMIT;
