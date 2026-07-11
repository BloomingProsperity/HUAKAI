CREATE TABLE settlement_intents (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    request_id          text          NOT NULL,
    logical_request_id  text,
    attempt_seq         integer       NOT NULL,
    claim_id            bigint        NOT NULL,
    api_key_id          bigint,
    request_fingerprint text          NOT NULL,
    status              text          NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'delivering', 'settling', 'settled', 'aborted', 'failed', 'superseded')),
    predicted_cost      numeric(20,8) NOT NULL DEFAULT 0,
    actual_cost         numeric(20,8) NOT NULL DEFAULT 0,
    hold_id             bigint,
    first_byte_at       timestamptz,
    retry_count         integer       NOT NULL DEFAULT 0,
    version             integer       NOT NULL DEFAULT 0,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    settled_at          timestamptz,
    CONSTRAINT settlement_intents_claim_fk
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT settlement_intents_claim_attempt_key
        UNIQUE (tenant_id, claim_id, attempt_seq),
    CONSTRAINT settlement_intents_attempt_seq_positive
        CHECK (attempt_seq >= 1),
    CONSTRAINT settlement_intents_version_nonnegative
        CHECK (version >= 0),
    CONSTRAINT settlement_intents_retry_count_nonnegative
        CHECK (retry_count >= 0),
    CONSTRAINT settlement_intents_predicted_cost_nonnegative
        CHECK (predicted_cost >= 0),
    CONSTRAINT settlement_intents_actual_cost_nonnegative
        CHECK (actual_cost >= 0)
);

CREATE INDEX idx_settlement_intents_tenant_status
    ON settlement_intents (tenant_id, status);

CREATE INDEX idx_settlement_intents_claim_status
    ON settlement_intents (tenant_id, claim_id, status);
