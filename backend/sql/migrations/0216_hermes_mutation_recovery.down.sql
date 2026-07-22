BEGIN;

DROP TABLE IF EXISTS hermes_mutation_recovery;

DROP INDEX IF EXISTS admin_audit_events_operation_id_unique;
DROP INDEX IF EXISTS hermes_tool_calls_operation_id_unique;

ALTER TABLE admin_audit_events
    DROP COLUMN IF EXISTS operation_id;

ALTER TABLE hermes_tool_calls
    DROP COLUMN IF EXISTS operation_id;

COMMIT;
