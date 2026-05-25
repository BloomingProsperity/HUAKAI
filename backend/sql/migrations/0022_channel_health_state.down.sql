-- 0022_channel_health_state.down.sql
--
-- Roll back F-CH-002 additive channel health tables.

BEGIN;

DROP TABLE IF EXISTS channel_health_admin_alerts;
DROP TABLE IF EXISTS channel_health_audit_events;
DROP TABLE IF EXISTS channel_health_state;

COMMIT;
