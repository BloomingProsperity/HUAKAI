BEGIN;

ALTER TABLE referral_rewards
    DROP CONSTRAINT IF EXISTS referral_rewards_tenant_referral_unique,
    DROP COLUMN IF EXISTS currency_code,
    DROP COLUMN IF EXISTS billing_event_id;

COMMIT;
