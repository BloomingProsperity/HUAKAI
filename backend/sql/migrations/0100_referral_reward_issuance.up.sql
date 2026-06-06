BEGIN;

ALTER TABLE referral_rewards
    ALTER COLUMN receipt_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS billing_event_id BIGINT,
    ADD COLUMN IF NOT EXISTS currency_code TEXT NOT NULL DEFAULT 'USD';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'referral_rewards_tenant_referral_unique'
    ) THEN
        ALTER TABLE referral_rewards
            ADD CONSTRAINT referral_rewards_tenant_referral_unique UNIQUE (tenant_id, referral_id);
    END IF;
END $$;

COMMIT;
