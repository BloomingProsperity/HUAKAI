-- 0021_session_management.down.sql
--
-- Roll back F-SESSION-001 platform session tables.

BEGIN;

DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS session_tokens;
DROP TABLE IF EXISTS session_families;

COMMIT;
