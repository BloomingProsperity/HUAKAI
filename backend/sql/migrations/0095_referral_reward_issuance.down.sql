BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM payment_credits
        WHERE reason_class = 'referral_reward'
    ) THEN
        RAISE EXCEPTION 'cannot rollback 0094_referral_reward_issuance: referral reward payment credits exist';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM referral_rewards
        WHERE receipt_id IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot rollback 0094_referral_reward_issuance: receipt-less referral rewards exist';
    END IF;
END $$;

DROP TABLE IF EXISTS referral_reward_audit_events;

ALTER TABLE payment_credits
    DROP CONSTRAINT IF EXISTS payment_credits_reason_class_check,
    ADD CONSTRAINT payment_credits_reason_class_check
        CHECK (reason_class IN ('manual_confirmed', 'test_provider_paid'));

ALTER TABLE referral_rewards
    ALTER COLUMN receipt_id SET NOT NULL;

DROP INDEX IF EXISTS uq_referral_rewards_referral;
DROP INDEX IF EXISTS uq_referral_rewards_tenant_id;

COMMIT;
