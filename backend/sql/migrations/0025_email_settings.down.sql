-- 0025_email_settings.down.sql
--
-- Roll back additive F-AUTH-007 email delivery settings.

BEGIN;

DROP TABLE IF EXISTS email_settings;

COMMIT;
