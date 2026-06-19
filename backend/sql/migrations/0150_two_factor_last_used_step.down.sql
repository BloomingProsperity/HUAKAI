BEGIN;

ALTER TABLE two_factor_settings
    DROP COLUMN IF EXISTS last_used_step;

COMMIT;
