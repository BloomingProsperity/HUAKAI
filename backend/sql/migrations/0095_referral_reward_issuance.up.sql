BEGIN;

-- C6 referral reward issuance guard: one durable reward fact per referral.
CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_rewards_referral
    ON referral_rewards (tenant_id, referral_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_rewards_tenant_id
    ON referral_rewards (tenant_id, id);

-- C6 credit rewards are not request-cost receipts; the reward audit table below
-- carries the issuance trail while user_cost_receipts remains request billing proof.
ALTER TABLE referral_rewards
    ALTER COLUMN receipt_id DROP NOT NULL;

ALTER TABLE payment_credits
    DROP CONSTRAINT IF EXISTS payment_credits_reason_class_check,
    ADD CONSTRAINT payment_credits_reason_class_check
        CHECK (reason_class IN ('manual_confirmed', 'test_provider_paid', 'referral_reward'));

CREATE TABLE IF NOT EXISTS referral_reward_audit_events (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT NOT NULL REFERENCES tenants(id),
    referral_id       BIGINT,
    referrer_user_id  BIGINT,
    referee_user_id   BIGINT,
    billing_event_id  BIGINT,
    reward_id         BIGINT,
    payment_order_id  BIGINT,
    event_type        TEXT NOT NULL
        CHECK (event_type IN ('REWARD_ISSUED', 'REWARD_FAILED', 'REWARD_SKIPPED')),
    reason            TEXT,
    redacted_payload  JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, referral_id) REFERENCES referrals(tenant_id, id),
    FOREIGN KEY (tenant_id, reward_id) REFERENCES referral_rewards(tenant_id, id),
    FOREIGN KEY (tenant_id, payment_order_id) REFERENCES payment_orders(tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_referral_reward_audit_referral
    ON referral_reward_audit_events (tenant_id, referral_id, occurred_at DESC)
    WHERE referral_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_referral_reward_audit_referrer
    ON referral_reward_audit_events (tenant_id, referrer_user_id, occurred_at DESC)
    WHERE referrer_user_id IS NOT NULL;

COMMIT;
