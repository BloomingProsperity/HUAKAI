CREATE TABLE IF NOT EXISTS user_balances (
    tenant_id   bigint        NOT NULL,
    user_id     bigint        NOT NULL,
    balance     numeric(20,8) NOT NULL DEFAULT 0,
    held        numeric(20,8) NOT NULL DEFAULT 0 CHECK (held >= 0),
    version     bigint        NOT NULL DEFAULT 0,
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS balance_holds (
    claim_id    bigint        PRIMARY KEY REFERENCES billing_ledger_claims (id),
    tenant_id   bigint        NOT NULL,
    user_id     bigint        NOT NULL,
    amount      numeric(20,8) NOT NULL CHECK (amount >= 0),
    captured    numeric(20,8) NOT NULL DEFAULT 0,
    state       text          NOT NULL DEFAULT 'held' CHECK (state IN ('held', 'captured', 'released')),
    created_at  timestamptz   NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_balance_holds_user_state ON balance_holds (tenant_id, user_id, state) WHERE state = 'held';
