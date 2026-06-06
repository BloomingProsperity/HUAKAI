-- Roll back HUAKAI announcements board storage.

BEGIN;

DROP TABLE IF EXISTS announcements;

COMMIT;
