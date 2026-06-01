-- 0064_balance_enforcement_mode.down.sql
--
-- This removes only the explicit mandatory setting and restores the previous
-- billing_settings value check. The pre-flip balance reconciliation in 0064 is
-- live money state and is intentionally not destructively rolled back.

BEGIN;

DELETE FROM billing_settings
WHERE setting_key = 'balance_enforcement_mode'
  AND updated_by = 'migration:0064';

ALTER TABLE billing_settings
    DROP CONSTRAINT IF EXISTS billing_settings_check,
    DROP CONSTRAINT IF EXISTS billing_settings_setting_value_check,
    ADD CONSTRAINT billing_settings_setting_value_check
        CHECK (
            setting_key <> 'stream_input_only_interrupted_policy'
            OR setting_value IN ('no_bill', 'no_bill_record')
        );

COMMIT;
