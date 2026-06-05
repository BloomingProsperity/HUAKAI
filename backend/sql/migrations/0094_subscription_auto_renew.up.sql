-- C5 cancel-renew: persisted self-service auto-renew flag.

BEGIN;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS auto_renew boolean NOT NULL DEFAULT true;

COMMIT;
