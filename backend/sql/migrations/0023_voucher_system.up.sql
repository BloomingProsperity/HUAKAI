-- 0023_voucher_system.up.sql
--
-- F-BILL-002 voucher system. Additive tenant-scoped commercial foundation:
-- voucher batches, voucher codes, redemption records, and redemption burst
-- blocks. Raw voucher codes are not stored; code_hash/code_fingerprint only.

BEGIN;

CREATE TABLE IF NOT EXISTS voucher_batch (
    id                              bigserial PRIMARY KEY,
    tenant_id                       bigint      NOT NULL REFERENCES tenants(id),
    created_by_admin_id             bigint,
    requested_count                 integer     NOT NULL CHECK (requested_count > 0),
    created_count                   integer     NOT NULL DEFAULT 0 CHECK (created_count >= 0),
    amount_cents                    bigint      NOT NULL CHECK (amount_cents > 0),
    currency_code                   char(3)     NOT NULL DEFAULT 'USD',
    valid_from                      timestamptz NOT NULL,
    valid_until                     timestamptz NOT NULL,
    max_redemptions                 integer     NOT NULL DEFAULT 1 CHECK (max_redemptions > 0),
    single_use_per_user             boolean     NOT NULL DEFAULT true,
    status                          text        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'completed', 'failed', 'revoked')),
    metadata                        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    CHECK (valid_until > valid_from),
    CHECK (created_count <= requested_count)
);

CREATE INDEX IF NOT EXISTS idx_voucher_batch_tenant_created
    ON voucher_batch (tenant_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_batch_tenant_id
    ON voucher_batch (tenant_id, id);

CREATE TABLE IF NOT EXISTS voucher (
    id                              bigserial PRIMARY KEY,
    tenant_id                       bigint      NOT NULL REFERENCES tenants(id),
    batch_id                        bigint,
    code_hash                       bytea       NOT NULL,
    code_fingerprint                text        NOT NULL,
    amount_cents                    bigint      NOT NULL CHECK (amount_cents > 0),
    currency_code                   char(3)     NOT NULL DEFAULT 'USD',
    valid_from                      timestamptz NOT NULL,
    valid_until                     timestamptz NOT NULL,
    max_redemptions                 integer     NOT NULL DEFAULT 1 CHECK (max_redemptions > 0),
    redeemed_count                  integer     NOT NULL DEFAULT 0 CHECK (redeemed_count >= 0),
    single_use_per_user             boolean     NOT NULL DEFAULT true,
    eligible_user_id                bigint,
    status                          text        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'expired', 'exhausted', 'revoked')),
    created_by_admin_id             bigint,
    revoked_by_admin_id             bigint,
    revoked_reason                  text,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    updated_at                      timestamptz NOT NULL DEFAULT now(),
    revoked_at                      timestamptz,
    FOREIGN KEY (tenant_id, batch_id) REFERENCES voucher_batch (tenant_id, id),
    FOREIGN KEY (tenant_id, eligible_user_id) REFERENCES users (tenant_id, id),
    CHECK (valid_until > valid_from),
    CHECK (redeemed_count <= max_redemptions)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_tenant_code_hash
    ON voucher (tenant_id, code_hash);
CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_tenant_id
    ON voucher (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_voucher_tenant_status_window
    ON voucher (tenant_id, status, valid_until);
CREATE INDEX IF NOT EXISTS idx_voucher_batch
    ON voucher (tenant_id, batch_id, id)
    WHERE batch_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS voucher_redemption (
    id                              bigserial PRIMARY KEY,
    tenant_id                       bigint      NOT NULL REFERENCES tenants(id),
    voucher_id                      bigint      NOT NULL,
    user_id                         bigint      NOT NULL,
    idempotency_key                 text,
    amount_cents                    bigint      NOT NULL CHECK (amount_cents > 0),
    currency_code                   char(3)     NOT NULL DEFAULT 'USD',
    single_use_per_user             boolean     NOT NULL DEFAULT true,
    status                          text        NOT NULL DEFAULT 'succeeded'
        CHECK (status IN ('succeeded')),
    source_ip_hash                  text        NOT NULL DEFAULT '',
    request_id                      text,
    billing_event_id                bigint,
    redeemed_at                     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, voucher_id) REFERENCES voucher (tenant_id, id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_redemption_idempotency
    ON voucher_redemption (tenant_id, user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_single_use_user
    ON voucher_redemption (tenant_id, voucher_id, user_id)
    WHERE single_use_per_user;
CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_redemption_tenant_id
    ON voucher_redemption (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_voucher_redemption_user_time
    ON voucher_redemption (tenant_id, user_id, redeemed_at DESC);

CREATE TABLE IF NOT EXISTS voucher_burst_block (
    id                              bigserial PRIMARY KEY,
    tenant_id                       bigint      NOT NULL REFERENCES tenants(id),
    user_id                         bigint      NOT NULL,
    source_ip_hash                  text        NOT NULL,
    window_start                    timestamptz NOT NULL,
    attempts                        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    blocked_until                   timestamptz,
    reason_class                    text        NOT NULL DEFAULT 'attempt_burst',
    voucher_fingerprint             text,
    request_id                      text,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    updated_at                      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_voucher_burst_window
    ON voucher_burst_block (tenant_id, user_id, source_ip_hash, window_start);
CREATE INDEX IF NOT EXISTS idx_voucher_burst_block_active
    ON voucher_burst_block (tenant_id, blocked_until)
    WHERE blocked_until IS NOT NULL;

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS voucher_redemption_id bigint;

ALTER TABLE billing_events
    ALTER COLUMN claim_id DROP NOT NULL;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed'
        ));

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_voucher_redemption,
    ADD CONSTRAINT fk_billing_events_voucher_redemption
        FOREIGN KEY (tenant_id, voucher_redemption_id)
        REFERENCES voucher_redemption (tenant_id, id);

ALTER TABLE voucher_redemption
    DROP CONSTRAINT IF EXISTS fk_voucher_redemption_billing_event,
    ADD CONSTRAINT fk_voucher_redemption_billing_event
        FOREIGN KEY (billing_event_id)
        REFERENCES billing_events (id);

CREATE INDEX IF NOT EXISTS idx_billing_events_voucher_redemption
    ON billing_events (tenant_id, voucher_redemption_id)
    WHERE voucher_redemption_id IS NOT NULL;

COMMENT ON TABLE voucher_batch IS
    'F-BILL-002 admin-created voucher batch summary. Raw codes are never stored.';
COMMENT ON TABLE voucher IS
    'F-BILL-002 tenant-scoped voucher code hash and lifecycle. Raw code appears only in create response.';
COMMENT ON TABLE voucher_redemption IS
    'F-BILL-002 successful user voucher redemption. The paired billing_events row carries event_type=voucher_redeemed.';
COMMENT ON TABLE voucher_burst_block IS
    'F-BILL-002 per User+IP redemption attempt window and temporary burst block evidence.';
COMMENT ON COLUMN voucher.code_hash IS
    'SHA-256 hash over tenant scope and normalized code; raw voucher code is never stored.';
COMMENT ON COLUMN voucher.code_fingerprint IS
    'Short non-secret fingerprint for audit/support correlation; not redeemable.';
COMMENT ON COLUMN billing_events.voucher_redemption_id IS
    'F-BILL-002 top-up event link. Non-null only when event_type=voucher_redeemed.';

COMMIT;
