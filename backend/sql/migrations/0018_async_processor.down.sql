-- 0018_async_processor.down.sql
--
-- Conservative rollback for F-OBS-004 v1 event ledger. Runtime can fall back
-- to direct critical-prefix settlement when this table is absent.

BEGIN;

DROP TABLE IF EXISTS async_processor_events;

COMMIT;
