BEGIN;

-- F-COMM-001 Phase 1: 邀请码、推荐绑定、奖励记录、tier 进度四张业务表。
CREATE TABLE invitations (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    code TEXT NOT NULL UNIQUE,
    inviter_user_id BIGINT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    expires_at TIMESTAMP WITH TIME ZONE,
    usage_count INTEGER NOT NULL DEFAULT 0 CHECK (usage_count >= 0),
    max_usage INTEGER NOT NULL DEFAULT 1 CHECK (max_usage > 0),
    client_idempotency_key TEXT,
    CONSTRAINT invitations_tenant_id_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT invitations_usage_within_max CHECK (usage_count <= max_usage)
);
CREATE INDEX idx_invitations_inviter ON invitations(inviter_user_id);
CREATE INDEX idx_invitations_tenant_created ON invitations(tenant_id, created_at);
CREATE UNIQUE INDEX idx_invitations_tenant_client_idempotency
    ON invitations(tenant_id, client_idempotency_key)
    WHERE client_idempotency_key IS NOT NULL;

CREATE TABLE referrals (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    referee_user_id BIGINT NOT NULL UNIQUE,
    referrer_user_id BIGINT NOT NULL,
    invitation_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'qualified', 'rewarded', 'rejected')),
    qualified_at TIMESTAMP WITH TIME ZONE,
    first_billing_event_id BIGINT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT referrals_tenant_id_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT referrals_invitation_tenant_fk FOREIGN KEY (tenant_id, invitation_id) REFERENCES invitations(tenant_id, id)
);
CREATE INDEX idx_referrals_referrer ON referrals(referrer_user_id);
CREATE INDEX idx_referrals_status ON referrals(status);
CREATE UNIQUE INDEX idx_referrals_billing_event
    ON referrals(first_billing_event_id)
    WHERE first_billing_event_id IS NOT NULL;

CREATE TABLE referral_rewards (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    referrer_user_id BIGINT NOT NULL,
    referee_user_id BIGINT NOT NULL,
    referral_id BIGINT NOT NULL,
    reward_type TEXT NOT NULL CHECK (reward_type IN ('credit', 'voucher')),
    amount_usd_micros BIGINT NOT NULL CHECK (amount_usd_micros >= 0),
    receipt_id BIGINT NOT NULL REFERENCES user_cost_receipts(id),
    issued_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT referral_rewards_referral_tenant_fk FOREIGN KEY (tenant_id, referral_id) REFERENCES referrals(tenant_id, id)
);
CREATE INDEX idx_referral_rewards_referrer ON referral_rewards(referrer_user_id);

CREATE TABLE tier_progress (
    user_id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    total_qualified_referrals INTEGER NOT NULL DEFAULT 0 CHECK (total_qualified_referrals >= 0),
    current_tier TEXT NOT NULL DEFAULT 'none' CHECK (current_tier IN ('none', 'silver', 'gold', 'platinum')),
    tier_unlocked_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

COMMIT;
