BEGIN;

ALTER TABLE usage_records
    DROP COLUMN IF EXISTS cost_snapshot;

COMMIT;
