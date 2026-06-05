-- Roll back C5 cancel-renew auto-renew flag.

BEGIN;

ALTER TABLE user_subscriptions
    DROP COLUMN IF EXISTS auto_renew;

COMMIT;
