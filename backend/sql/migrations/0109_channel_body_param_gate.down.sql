-- 0109_channel_body_param_gate.down.sql

BEGIN;

ALTER TABLE channels
    DROP COLUMN IF EXISTS param_override,
    DROP COLUMN IF EXISTS body_param_strips;

COMMIT;
