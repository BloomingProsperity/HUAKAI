-- Roll back HUAKAI tenant-scoped alerting storage.

BEGIN;

DROP TABLE IF EXISTS alert_silences;
DROP TABLE IF EXISTS alert_events;
DROP TABLE IF EXISTS alert_rules;

COMMIT;
